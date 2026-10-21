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

package license

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const licenseData = `CPU: 17
Memory: 33792
Nodes: 5
conf:
  baton:
    cluster:
      public_vip: 172.63.0.2
endpoints:
  identity:
    auth:
      admin:
        password: secret
`

func cpconfig(data string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ecp.easystack.cn/v1",
		"kind":       "Cpconfig",
		"metadata": map[string]any{
			"name":      CloudProductName,
			"namespace": DefaultNamespace,
		},
	}}
	if data != "" {
		obj.Object["data"] = data
	}
	return obj
}

func newReader(t *testing.T, objects ...client.Object) *Reader {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(cpconfigGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(cpconfigGVK.GroupVersion().WithKind("CpconfigList"), &unstructured.UnstructuredList{})
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &Reader{Reader: fakeClient}
}

func TestCurrent(t *testing.T) {
	t.Run("all limits", func(t *testing.T) {
		limits, err := newReader(t, cpconfig(licenseData)).Current(context.Background())
		if err != nil {
			t.Fatalf("Current: %v", err)
		}
		if limits.UnlimitedCPU || limits.UnlimitedMemory || limits.UnlimitedNodes {
			t.Errorf("unexpected unlimited flags: %+v", limits)
		}
		if limits.CPU != 17 || limits.Memory != 33792 || limits.Nodes != 5 {
			t.Errorf("limits = %+v, want cpu 17, memory 33792, nodes 5", limits)
		}
	})

	t.Run("unlimited values", func(t *testing.T) {
		data := "CPU: Unlimited\nMemory: Unlimited\nNodes: Unlimited\n"
		limits, err := newReader(t, cpconfig(data)).Current(context.Background())
		if err != nil {
			t.Fatalf("Current: %v", err)
		}
		if !limits.UnlimitedCPU || !limits.UnlimitedMemory || !limits.UnlimitedNodes {
			t.Errorf("expected all unlimited, got %+v", limits)
		}
	})

	t.Run("missing keys default to unlimited", func(t *testing.T) {
		limits, err := newReader(t, cpconfig("conf: {}\n")).Current(context.Background())
		if err != nil {
			t.Fatalf("Current: %v", err)
		}
		if !limits.UnlimitedCPU || !limits.UnlimitedMemory || !limits.UnlimitedNodes {
			t.Errorf("expected all unlimited, got %+v", limits)
		}
	})

	t.Run("string numbers", func(t *testing.T) {
		data := "CPU: \"17\"\nMemory: \"33792\"\nNodes: \"5\"\n"
		limits, err := newReader(t, cpconfig(data)).Current(context.Background())
		if err != nil {
			t.Fatalf("Current: %v", err)
		}
		if limits.CPU != 17 || limits.Memory != 33792 || limits.Nodes != 5 {
			t.Errorf("limits = %+v", limits)
		}
	})

	t.Run("invalid value", func(t *testing.T) {
		data := "CPU: banana\n"
		if _, err := newReader(t, cpconfig(data)).Current(context.Background()); err == nil {
			t.Fatal("expected error for invalid value")
		}
	})

	t.Run("missing cpconfig", func(t *testing.T) {
		if _, err := newReader(t).Current(context.Background()); err == nil {
			t.Fatal("expected error for missing cpconfig")
		}
	})

	t.Run("missing data field", func(t *testing.T) {
		if _, err := newReader(t, cpconfig("")).Current(context.Background()); err == nil {
			t.Fatal("expected error for missing data field")
		}
	})
}
