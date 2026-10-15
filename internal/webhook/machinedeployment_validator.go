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

// Package webhook implements the admission webhooks of the operator.
package webhook

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	capov1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	"github.com/cluster-management/internal/license"
	"github.com/cluster-management/internal/openstack"
)

const (
	// MachineDeploymentWebhookPath is the path the MachineDeployment validating
	// webhook is registered on. Keep it in sync with the kubebuilder markers below.
	MachineDeploymentWebhookPath = "/validate-cluster-x-k8s-io-machinedeployment"

	openStackMachineTemplateKind = "OpenStackMachineTemplate"

	// usageRefreshPeriod is the interval of the periodic usage ledger recount.
	usageRefreshPeriod = 5 * time.Minute

	// flavorCacheTTL is how long the cached flavor catalog stays valid.
	flavorCacheTTL = 10 * time.Minute
)

// The webhook only registers for v1beta2 (the stored version): the typed decode
// cannot convert v1beta1 requests client-side, so v1beta1 requests are first
// converted by the apiserver via matchPolicy=Equivalent.
//
// create/update fail closed (license enforcement); delete only feeds the usage
// ledger and must never block cluster teardown during a webhook outage.
// +kubebuilder:webhook:path=/validate-cluster-x-k8s-io-machinedeployment,mutating=false,failurePolicy=fail,sideEffects=None,groups=cluster.x-k8s.io,resources=machinedeployments,verbs=create;update,versions=v1beta2,name=vmachinedeployment.ecns.easystack.io,admissionReviewVersions=v1,matchPolicy=Equivalent
// +kubebuilder:webhook:path=/validate-cluster-x-k8s-io-machinedeployment,mutating=false,failurePolicy=ignore,sideEffects=None,groups=cluster.x-k8s.io,resources=machinedeployments,verbs=delete,versions=v1beta2,name=vmachinedeployment-delete.ecns.easystack.io,admissionReviewVersions=v1,matchPolicy=Equivalent
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machinedeployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=openstackmachinetemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=ecp.easystack.cn,resources=cpconfigs,verbs=get

// MachineDeploymentValidator denies MachineDeployment creates and updates that
// would push the total resource usage of all clusters beyond the license
// limits. The product-wide usage is tracked in a locked in-memory ledger so
// that concurrent admissions cannot miss each other's increments; the ledger is
// initialized by a full recount on first use and reconciled periodically.
// The flavor catalog is cached locally (TTL) so repeated lookups do not query
// nova again. A single webhook replica is assumed (the cache is per-process).
type MachineDeploymentValidator struct {
	// Reader reads all MachineDeployments and their infrastructure templates.
	Reader client.Reader
	// FlavorLister lists the flavor catalog of the OpenStack cloud.
	FlavorLister openstack.FlavorLister
	// License reads the license limits.
	License *license.Reader
	// Decoder decodes admission requests against the manager scheme.
	Decoder admission.Decoder

	// ledgerMu guards the usage ledger.
	ledgerMu    sync.Mutex
	ledger      resourceUsage
	ledgerReady bool

	// flavorMu guards the flavor catalog cache.
	flavorMu  sync.Mutex
	flavors   map[string]openstack.Flavor
	flavorsAt time.Time
}

// SetupWithManager registers the webhook on the manager's webhook server.
func (v *MachineDeploymentValidator) SetupWithManager(mgr ctrl.Manager) error {
	mgr.GetWebhookServer().Register(MachineDeploymentWebhookPath, &admission.Webhook{Handler: v})
	return nil
}

// Start implements manager.Runnable: it periodically reconciles the usage
// ledger with a full recount of all MachineDeployments.
func (v *MachineDeploymentValidator) Start(ctx context.Context) error {
	ticker := time.NewTicker(usageRefreshPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			v.RefreshUsage(ctx)
		}
	}
}

// RefreshUsage recomputes the usage ledger from all MachineDeployments. The
// recount runs outside the ledger lock, so an admission delta applied between
// the recount and the swap can be lost until the next refresh; the resulting
// tiny skew is accepted and self-heals.
func (v *MachineDeploymentValidator) RefreshUsage(ctx context.Context) {
	usage, err := v.recountUsage(ctx)
	if err != nil {
		logf.FromContext(ctx).Error(err, "Failed to recount cluster resource usage")
		return
	}
	v.ledgerMu.Lock()
	v.ledger = usage
	v.ledgerReady = true
	v.ledgerMu.Unlock()
}

// Handle implements admission.Handler.
func (v *MachineDeploymentValidator) Handle(ctx context.Context, req admission.Request) admission.Response {
	logger := logf.FromContext(ctx)

	md := &clusterv1.MachineDeployment{}
	var delta int64
	switch req.Operation {
	case admissionv1.Create:
		if err := v.Decoder.Decode(req, md); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode machinedeployment: %w", err))
		}
		delta = int64(replicas(md))
	case admissionv1.Update:
		if err := v.Decoder.Decode(req, md); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode machinedeployment: %w", err))
		}
		oldMD := &clusterv1.MachineDeployment{}
		if err := v.Decoder.DecodeRaw(req.OldObject, oldMD); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode old machinedeployment: %w", err))
		}
		delta = int64(replicas(md)) - int64(replicas(oldMD))
	case admissionv1.Delete:
		if err := v.Decoder.DecodeRaw(req.OldObject, md); err != nil {
			return admission.Errored(http.StatusBadRequest, fmt.Errorf("decode deleted machinedeployment: %w", err))
		}
		delta = -int64(replicas(md))
	default:
		return admission.Allowed("operation is not validated")
	}

	if delta == 0 {
		return admission.Allowed("replica count is not changed")
	}

	// The MachineDeployment template is immutable, so the template flavor is
	// valid for the object under admission in every operation.
	flavor, err := v.machineTemplateFlavor(ctx, md)
	if err != nil {
		logger.Error(err, "Failed to resolve flavor for MachineDeployment admission",
			"machinedeployment", client.ObjectKeyFromObject(md).String())
		return admission.Denied(fmt.Sprintf("cannot verify license limits: %v", err))
	}
	deltaUsage := resourceUsage{}.withFlavor(flavor, delta)

	if delta < 0 {
		// Scale-downs and deletions are never blocked; a ledger update failure
		// only skews the accounting until the next refresh.
		if _, err := v.applyUsage(ctx, deltaUsage, nil); err != nil {
			logger.Error(err, "Failed to update usage ledger for replica decrease",
				"machinedeployment", client.ObjectKeyFromObject(md).String())
		}
		return admission.Allowed("replica count is not increased")
	}

	limits, err := v.License.Current(ctx)
	if err != nil {
		logger.Error(err, "Failed to read license for MachineDeployment admission",
			"machinedeployment", client.ObjectKeyFromObject(md).String())
		return admission.Denied(fmt.Sprintf("cannot read license: %v", err))
	}
	if limits.UnlimitedCPU && limits.UnlimitedMemory && limits.UnlimitedNodes {
		return admission.Allowed("license does not limit node resources")
	}

	message, err := v.applyUsage(ctx, deltaUsage, func(total resourceUsage) string {
		return exceeds(total, limits)
	})
	if err != nil {
		logger.Error(err, "Failed to update usage ledger",
			"machinedeployment", client.ObjectKeyFromObject(md).String())
		return admission.Denied(fmt.Sprintf("cannot verify license limits: %v", err))
	}
	if message != "" {
		logger.Info("Denied MachineDeployment for exceeding license limits",
			"machinedeployment", client.ObjectKeyFromObject(md).String(),
			"deltaCPU", deltaUsage.cpu, "deltaMemoryMiB", deltaUsage.memory, "deltaNodes", deltaUsage.nodes)
		return admission.Denied(message)
	}
	return admission.Allowed("within license limits")
}

// applyUsage atomically initializes the ledger on first use, checks the
// projected total with check (when non-nil) and applies the delta. On a
// non-empty check result or an error the ledger is left unchanged.
func (v *MachineDeploymentValidator) applyUsage(ctx context.Context, delta resourceUsage, check func(resourceUsage) string) (string, error) {
	v.ledgerMu.Lock()
	defer v.ledgerMu.Unlock()

	if !v.ledgerReady {
		usage, err := v.recountUsage(ctx)
		if err != nil {
			return "", err
		}
		v.ledger = usage
		v.ledgerReady = true
	}

	total := v.ledger
	total.cpu += delta.cpu
	total.memory += delta.memory
	total.nodes += delta.nodes
	if check != nil {
		if message := check(total); message != "" {
			return message, nil
		}
	}
	v.ledger = total
	return "", nil
}

// recountUsage computes the resource usage of all MachineDeployments across all
// clusters.
func (v *MachineDeploymentValidator) recountUsage(ctx context.Context) (resourceUsage, error) {
	list := &clusterv1.MachineDeploymentList{}
	if err := v.Reader.List(ctx, list); err != nil {
		return resourceUsage{}, fmt.Errorf("list machinedeployments: %w", err)
	}

	var total resourceUsage
	for i := range list.Items {
		item := &list.Items[i]
		count := int64(replicas(item))
		if count == 0 {
			continue
		}
		flavor, err := v.machineTemplateFlavor(ctx, item)
		if err != nil {
			return resourceUsage{}, err
		}
		total = total.withFlavor(flavor, count)
	}
	return total, nil
}

// withFlavor returns the usage of count replicas of the flavor added to u.
func (u resourceUsage) withFlavor(flavor openstack.Flavor, count int64) resourceUsage {
	u.cpu += flavor.CPU * count
	u.memory += flavor.MemoryMiB * count
	u.nodes += count
	return u
}

type resourceUsage struct {
	cpu    int64
	memory int64
	nodes  int64
}

// machineTemplateFlavor resolves the per-replica flavor of a MachineDeployment
// from its infrastructure template.
func (v *MachineDeploymentValidator) machineTemplateFlavor(ctx context.Context, md *clusterv1.MachineDeployment) (openstack.Flavor, error) {
	ref := md.Spec.Template.Spec.InfrastructureRef
	if ref.Kind != openStackMachineTemplateKind || ref.APIGroup != capov1.GroupName {
		return openstack.Flavor{}, fmt.Errorf("machinedeployment %s/%s has unsupported infrastructure reference %s/%s",
			md.Namespace, md.Name, ref.APIGroup, ref.Kind)
	}

	template := &capov1.OpenStackMachineTemplate{}
	if err := v.Reader.Get(ctx, client.ObjectKey{Namespace: md.Namespace, Name: ref.Name}, template); err != nil {
		return openstack.Flavor{}, fmt.Errorf("get %s %s/%s: %w", ref.Kind, md.Namespace, ref.Name, err)
	}
	flavor := template.Spec.Template.Spec.Flavor
	if flavor == nil || *flavor == "" {
		return openstack.Flavor{}, fmt.Errorf("openstackmachinetemplate %s/%s has no spec.template.spec.flavor", md.Namespace, ref.Name)
	}
	return v.flavor(ctx, *flavor)
}

// flavor returns the flavor spec by name, serving from the catalog cache
// within flavorCacheTTL and refreshing the whole catalog otherwise.
func (v *MachineDeploymentValidator) flavor(ctx context.Context, name string) (openstack.Flavor, error) {
	v.flavorMu.Lock()
	if flavor, ok := v.flavors[name]; ok && time.Since(v.flavorsAt) < flavorCacheTTL {
		v.flavorMu.Unlock()
		return flavor, nil
	}
	v.flavorMu.Unlock()

	catalog, err := v.FlavorLister.ListFlavors(ctx)
	if err != nil {
		return openstack.Flavor{}, err
	}

	v.flavorMu.Lock()
	v.flavors = catalog
	v.flavorsAt = time.Now()
	v.flavorMu.Unlock()

	flavor, ok := catalog[name]
	if !ok {
		return openstack.Flavor{}, fmt.Errorf("flavor %q not found", name)
	}
	return flavor, nil
}

// replicas returns the desired replica count, defaulting to 1 like CAPI does.
func replicas(md *clusterv1.MachineDeployment) int32 {
	if md.Spec.Replicas == nil {
		return 1
	}
	return *md.Spec.Replicas
}

func exceeds(usage resourceUsage, limits license.Limits) string {
	var exceeded []string
	if !limits.UnlimitedNodes && usage.nodes > limits.Nodes {
		exceeded = append(exceeded, fmt.Sprintf("nodes %d > %d", usage.nodes, limits.Nodes))
	}
	if !limits.UnlimitedCPU && usage.cpu > limits.CPU {
		exceeded = append(exceeded, fmt.Sprintf("cpu %d > %d", usage.cpu, limits.CPU))
	}
	if !limits.UnlimitedMemory && usage.memory > limits.Memory {
		exceeded = append(exceeded, fmt.Sprintf("memory %dMi > %dMi", usage.memory, limits.Memory))
	}
	if len(exceeded) == 0 {
		return ""
	}
	return fmt.Sprintf("license limit exceeded: %s", strings.Join(exceeded, ", "))
}
