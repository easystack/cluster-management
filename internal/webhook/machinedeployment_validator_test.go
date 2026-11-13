/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/cluster-management/internal/license"
	"github.com/cluster-management/internal/openstack"
)

const testLicenseData = `CPU: 17
Memory: 33792
Nodes: 5
conf:
  baton:
    cluster:
      public_vip: 172.63.0.2
`

// The cpconfig API has no importable Go module, so the license reader (and its
// test fixtures) stay unstructured.
var cpGVK = schema.GroupVersionKind{Group: "ecp.easystack.cn", Version: "v1", Kind: "Cpconfig"}

type fakeFlavorLister struct {
	flavors map[string]openstack.Flavor
	err     error
	calls   int
}

func (f *fakeFlavorLister) ListFlavors(_ context.Context) (map[string]openstack.Flavor, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.flavors, nil
}

func machineDeployment(name string, replicas int32, templateName string) *clusterv1.MachineDeployment {
	return &clusterv1.MachineDeployment{
		TypeMeta: metav1.TypeMeta{APIVersion: clusterv1.GroupVersion.String(), Kind: "MachineDeployment"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "launcher",
		},
		Spec: clusterv1.MachineDeploymentSpec{
			Replicas: new(replicas),
			Template: clusterv1.MachineTemplateSpec{
				Spec: clusterv1.MachineSpec{
					InfrastructureRef: clusterv1.ContractVersionedObjectReference{
						APIGroup: capov1.GroupName,
						Kind:     "OpenStackMachineTemplate",
						Name:     templateName,
					},
				},
			},
		},
	}
}

func machineTemplate(name, flavor string) *capov1.OpenStackMachineTemplate {
	return &capov1.OpenStackMachineTemplate{
		TypeMeta: metav1.TypeMeta{APIVersion: capov1.SchemeGroupVersion.String(), Kind: "OpenStackMachineTemplate"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "launcher",
		},
		Spec: capov1.OpenStackMachineTemplateSpec{
			Template: capov1.OpenStackMachineTemplateResource{
				Spec: capov1.OpenStackMachineSpec{Flavor: new(flavor)},
			},
		},
	}
}

func cpconfig(data string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": cpGVK.GroupVersion().String(),
		"kind":       cpGVK.Kind,
		"metadata":   map[string]any{"name": license.CloudProductName, "namespace": license.DefaultNamespace},
		"data":       data,
	}}
}

func newValidator(t *testing.T, resolver openstack.FlavorLister, objects ...client.Object) *MachineDeploymentValidator {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clusterv1.AddToScheme(scheme); err != nil {
		t.Fatalf("add cluster-api scheme: %v", err)
	}
	if err := capov1.AddToScheme(scheme); err != nil {
		t.Fatalf("add capo scheme: %v", err)
	}
	scheme.AddKnownTypeWithName(cpGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(cpGVK.GroupVersion().WithKind("CpconfigList"), &unstructured.UnstructuredList{})

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &MachineDeploymentValidator{
		Reader:       fakeClient,
		FlavorLister: resolver,
		License:      &license.Reader{Reader: fakeClient},
		Decoder:      admission.NewDecoder(scheme),
	}
}

func request(t *testing.T, operation admissionv1.Operation, md, oldMD *clusterv1.MachineDeployment) admission.Request {
	t.Helper()
	raw := func(obj *clusterv1.MachineDeployment) []byte {
		if obj == nil {
			return nil
		}
		data, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("marshal object: %v", err)
		}
		return data
	}
	return admission.Request{AdmissionRequest: admissionv1.AdmissionRequest{
		Operation: operation,
		Namespace: md.Namespace,
		Name:      md.Name,
		Object:    runtime.RawExtension{Raw: raw(md)},
		OldObject: runtime.RawExtension{Raw: raw(oldMD)},
	}}
}

func dryRunRequest(t *testing.T, operation admissionv1.Operation, md, oldMD *clusterv1.MachineDeployment) admission.Request {
	t.Helper()
	req := request(t, operation, md, oldMD)
	dryRun := true
	req.DryRun = &dryRun
	return req
}

func snapshotLedger(v *MachineDeploymentValidator) (resourceUsage, bool) {
	v.ledgerMu.Lock()
	defer v.ledgerMu.Unlock()
	return v.ledger, v.ledgerReady
}

func flavor4C8G() map[string]openstack.Flavor {
	return map[string]openstack.Flavor{
		"4C-8G": {Name: "4C-8G", CPU: 4, MemoryMiB: 8192},
		"1C-1G": {Name: "1C-1G", CPU: 1, MemoryMiB: 1024},
	}
}

func TestMachineDeploymentValidator(t *testing.T) {
	ctx := context.Background()

	t.Run("update without replica increase is allowed without resolving flavors", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		old := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		validator := newValidator(t, resolver, cpconfig(testLicenseData), old, machineTemplate("m2-worker-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
		if resolver.calls != 0 {
			t.Errorf("resolver calls = %d, want 0", resolver.calls)
		}
	})

	t.Run("scale down is allowed", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		old := machineDeployment("m2-worker", 3, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 2, "m2-worker-tpl")
		validator := newValidator(t, resolver, cpconfig(testLicenseData), old, machineTemplate("m2-worker-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
	})

	t.Run("create within limits is allowed", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		existing := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-new", 1, "m2-new-tpl")
		validator := newValidator(t, resolver,
			cpconfig(testLicenseData),
			existing, machineTemplate("m2-worker-tpl", "4C-8G"),
			machineTemplate("m2-new-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Create, newMD, nil))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
	})

	t.Run("update replaces the stored replicas instead of double counting", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		// Existing: worker 1 + other 2 = 3 nodes / 12 cpu; scaling worker 1->2
		// gives 4 nodes / 16 cpu <= 17. Double counting would give 20 cpu.
		old := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 2, "m2-worker-tpl")
		other := machineDeployment("m2-other", 2, "m2-other-tpl")
		validator := newValidator(t, resolver,
			cpconfig(testLicenseData),
			old, other,
			machineTemplate("m2-worker-tpl", "4C-8G"),
			machineTemplate("m2-other-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
	})

	t.Run("update exceeding cpu is denied", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		old := machineDeployment("m2-worker", 4, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 5, "m2-worker-tpl")
		validator := newValidator(t, resolver, cpconfig(testLicenseData), old, machineTemplate("m2-worker-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if resp.Allowed {
			t.Fatal("expected denial")
		}
		if message := resp.Result.Message; !strings.Contains(message, "cpu 20 > 17") {
			t.Errorf("message = %q, want it to contain %q", message, "cpu 20 > 17")
		}
	})

	t.Run("create exceeding node count is denied", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		mds := []client.Object{
			cpconfig(testLicenseData),
			machineDeployment("m2-a", 3, "tpl-a"),
			machineDeployment("m2-b", 2, "tpl-b"),
			machineTemplate("tpl-a", "1C-1G"),
			machineTemplate("tpl-b", "1C-1G"),
			machineTemplate("tpl-c", "1C-1G"),
		}
		validator := newValidator(t, resolver, mds...)

		resp := validator.Handle(ctx, request(t, admissionv1.Create, machineDeployment("m2-c", 1, "tpl-c"), nil))
		if resp.Allowed {
			t.Fatal("expected denial")
		}
		if message := resp.Result.Message; !strings.Contains(message, "nodes 6 > 5") {
			t.Errorf("message = %q, want it to contain %q", message, "nodes 6 > 5")
		}
	})

	t.Run("resolver failure on increase fails closed", func(t *testing.T) {
		resolver := &fakeFlavorLister{err: errors.New("openstack unreachable")}
		old := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 2, "m2-worker-tpl")
		validator := newValidator(t, resolver, cpconfig(testLicenseData), old, machineTemplate("m2-worker-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if resp.Allowed {
			t.Fatal("expected denial")
		}
		if message := resp.Result.Message; !strings.Contains(message, "cannot verify license limits") {
			t.Errorf("message = %q, want it to contain %q", message, "cannot verify license limits")
		}
	})

	t.Run("unlimited license is allowed", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		old := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 100, "m2-worker-tpl")
		data := "CPU: Unlimited\nMemory: Unlimited\nNodes: Unlimited\n"
		validator := newValidator(t, resolver, cpconfig(data), old, machineTemplate("m2-worker-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
	})

	t.Run("license without limits denies any increase", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		validator := newValidator(t, resolver,
			cpconfig("conf: {}\n"),
			machineTemplate("m2-new-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Create, machineDeployment("m2-new", 1, "m2-new-tpl"), nil))
		if resp.Allowed {
			t.Fatal("expected denial")
		}
		for _, want := range []string{"nodes 1 > 0", "cpu 4 > 0", "memory 8192Mi > 0Mi"} {
			if message := resp.Result.Message; !strings.Contains(message, want) {
				t.Errorf("message = %q, want it to contain %q", message, want)
			}
		}
	})

	t.Run("missing cpconfig fails closed", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		old := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 2, "m2-worker-tpl")
		validator := newValidator(t, resolver, old, machineTemplate("m2-worker-tpl", "4C-8G"))

		resp := validator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if resp.Allowed {
			t.Fatal("expected denial")
		}
		if message := resp.Result.Message; !strings.Contains(message, "cannot read license") {
			t.Errorf("message = %q, want it to contain %q", message, "cannot read license")
		}
	})

	t.Run("dry run create does not update the ledger", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		existing := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-new", 1, "m2-new-tpl")
		validator := newValidator(t, resolver,
			cpconfig(testLicenseData),
			existing, machineTemplate("m2-worker-tpl", "4C-8G"),
			machineTemplate("m2-new-tpl", "4C-8G"))

		resp := validator.Handle(ctx, dryRunRequest(t, admissionv1.Create, newMD, nil))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
		if usage, ready := snapshotLedger(validator); ready || usage != (resourceUsage{}) {
			t.Fatalf("dry run mutated the ledger: usage=%+v ready=%v", usage, ready)
		}

		resp = validator.Handle(ctx, request(t, admissionv1.Create, newMD, nil))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
		if usage, ready := snapshotLedger(validator); !ready || usage != (resourceUsage{cpu: 8, memory: 16384, nodes: 2}) {
			t.Fatalf("ledger = %+v (ready=%v), want {cpu:8 memory:16384 nodes:2} (ready=true)", usage, ready)
		}
	})

	t.Run("dry run exceeding limits is denied like a real request", func(t *testing.T) {
		old := machineDeployment("m2-worker", 4, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 5, "m2-worker-tpl")

		dryValidator := newValidator(t, &fakeFlavorLister{flavors: flavor4C8G()},
			cpconfig(testLicenseData), old, machineTemplate("m2-worker-tpl", "4C-8G"))
		dryResp := dryValidator.Handle(ctx, dryRunRequest(t, admissionv1.Update, newMD, old))
		if dryResp.Allowed {
			t.Fatal("expected dry run denial")
		}

		realValidator := newValidator(t, &fakeFlavorLister{flavors: flavor4C8G()},
			cpconfig(testLicenseData), old, machineTemplate("m2-worker-tpl", "4C-8G"))
		realResp := realValidator.Handle(ctx, request(t, admissionv1.Update, newMD, old))
		if realResp.Allowed {
			t.Fatal("expected real request denial")
		}

		if dryResp.Result.Message != realResp.Result.Message {
			t.Errorf("dry run message = %q, real message = %q, want identical", dryResp.Result.Message, realResp.Result.Message)
		}
		if usage, ready := snapshotLedger(dryValidator); ready || usage != (resourceUsage{}) {
			t.Errorf("dry run mutated the ledger: usage=%+v ready=%v", usage, ready)
		}
	})

	t.Run("dry run scale down does not update the ledger", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		stored := machineDeployment("m2-worker", 3, "m2-worker-tpl")
		validator := newValidator(t, resolver, cpconfig(testLicenseData), stored, machineTemplate("m2-worker-tpl", "4C-8G"))

		two := machineDeployment("m2-worker", 2, "m2-worker-tpl")
		resp := validator.Handle(ctx, request(t, admissionv1.Update, two, stored))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
		if usage, ready := snapshotLedger(validator); !ready || usage != (resourceUsage{cpu: 8, memory: 16384, nodes: 2}) {
			t.Fatalf("ledger after real scale down = %+v (ready=%v)", usage, ready)
		}

		one := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		resp = validator.Handle(ctx, dryRunRequest(t, admissionv1.Update, one, two))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
		if usage, ready := snapshotLedger(validator); !ready || usage != (resourceUsage{cpu: 8, memory: 16384, nodes: 2}) {
			t.Fatalf("dry run scale down mutated the ledger: usage=%+v ready=%v", usage, ready)
		}
	})

	t.Run("dry run delete does not update the ledger", func(t *testing.T) {
		resolver := &fakeFlavorLister{flavors: flavor4C8G()}
		worker := machineDeployment("m2-worker", 2, "m2-worker-tpl")
		keeper := machineDeployment("m2-keep", 1, "m2-keep-tpl")
		validator := newValidator(t, resolver,
			cpconfig(testLicenseData), worker, keeper,
			machineTemplate("m2-worker-tpl", "4C-8G"), machineTemplate("m2-keep-tpl", "4C-8G"))

		validator.RefreshUsage(ctx)
		if usage, ready := snapshotLedger(validator); !ready || usage != (resourceUsage{cpu: 12, memory: 24576, nodes: 3}) {
			t.Fatalf("ledger after refresh = %+v (ready=%v)", usage, ready)
		}

		resp := validator.Handle(ctx, dryRunRequest(t, admissionv1.Delete, worker, worker))
		if !resp.Allowed {
			t.Fatalf("expected allowed, got %q", resp.Result.Message)
		}
		if usage, ready := snapshotLedger(validator); !ready || usage != (resourceUsage{cpu: 12, memory: 24576, nodes: 3}) {
			t.Fatalf("dry run delete mutated the ledger: usage=%+v ready=%v", usage, ready)
		}
	})

	t.Run("dry run still fails closed", func(t *testing.T) {
		old := machineDeployment("m2-worker", 1, "m2-worker-tpl")
		newMD := machineDeployment("m2-worker", 2, "m2-worker-tpl")

		unreachable := newValidator(t, &fakeFlavorLister{err: errors.New("openstack unreachable")},
			cpconfig(testLicenseData), old, machineTemplate("m2-worker-tpl", "4C-8G"))
		resp := unreachable.Handle(ctx, dryRunRequest(t, admissionv1.Update, newMD, old))
		if resp.Allowed {
			t.Fatal("expected dry run denial on flavor resolution failure")
		}
		if message := resp.Result.Message; !strings.Contains(message, "cannot verify license limits") {
			t.Errorf("message = %q, want it to contain %q", message, "cannot verify license limits")
		}

		noLicense := newValidator(t, &fakeFlavorLister{flavors: flavor4C8G()},
			machineTemplate("m2-new-tpl", "4C-8G"))
		resp = noLicense.Handle(ctx, dryRunRequest(t, admissionv1.Create, machineDeployment("m2-new", 1, "m2-new-tpl"), nil))
		if resp.Allowed {
			t.Fatal("expected dry run denial on missing license")
		}
		if message := resp.Result.Message; !strings.Contains(message, "cannot read license") {
			t.Errorf("message = %q, want it to contain %q", message, "cannot read license")
		}
	})
}
