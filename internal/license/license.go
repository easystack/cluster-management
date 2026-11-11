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

// Package license reads the resource limits of the product license from the
// Cpconfig object of the ECP platform. The limits are the top-level CPU,
// Memory and Nodes keys of the Cpconfig data. Only an explicit "Unlimited"
// value leaves a resource unlimited; a missing, null, negative, non-numeric or
// otherwise invalid value counts as zero, which allows no use of the resource.
package license

import (
	"context"
	"fmt"
	"strconv"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// DefaultNamespace is the namespace of the Cpconfig holding the managed
	// kubernetes (baton) license.
	DefaultNamespace = "ems"
	// CloudProductName is the name of the cloud product for which to read license limits.
	CloudProductName = "baton"

	unlimitedValue = "Unlimited"
)

var cpconfigGVK = schema.GroupVersionKind{Group: "ecp.easystack.cn", Version: "v1", Kind: "Cpconfig"}

// Limits is the resource limit of the license. A field whose Unlimited flag is
// set imposes no limit on that resource. A zero value with the flag unset
// allows no use of that resource at all.
type Limits struct {
	CPU    int64
	Memory int64
	Nodes  int64

	UnlimitedCPU    bool
	UnlimitedMemory bool
	UnlimitedNodes  bool
}

// Reader reads the license limits from the Cpconfig object.
type Reader struct {
	// Reader reads the Cpconfig object. Using the API reader (not the cached
	// client) avoids starting an informer for this type.
	client.Reader
	// Namespace and Name locate the Cpconfig object, defaulting to ems/baton.
	Namespace string
	Name      string
}

// Current returns the current license limits. Updating the Cpconfig is picked
// up on the next call.
func (r *Reader) Current(ctx context.Context) (Limits, error) {
	cpconfig := &unstructured.Unstructured{}
	cpconfig.SetGroupVersionKind(cpconfigGVK)
	key := types.NamespacedName{Namespace: r.namespace(), Name: r.name()}
	if err := r.Get(ctx, key, cpconfig); err != nil {
		return Limits{}, fmt.Errorf("get cpconfig %s: %w", key, err)
	}

	data, found, err := unstructured.NestedString(cpconfig.Object, "data")
	if err != nil {
		return Limits{}, fmt.Errorf("read data of cpconfig %s: %w", key, err)
	}
	if !found {
		return Limits{}, fmt.Errorf("cpconfig %s has no data", key)
	}

	var config map[string]any
	if err := yaml.Unmarshal([]byte(data), &config); err != nil {
		return Limits{}, fmt.Errorf("parse data of cpconfig %s: %w", key, err)
	}

	var limits Limits
	limits.CPU, limits.UnlimitedCPU = readLimit(config, "CPU")
	limits.Memory, limits.UnlimitedMemory = readLimit(config, "Memory")
	limits.Nodes, limits.UnlimitedNodes = readLimit(config, "Nodes")
	return limits, nil
}

func (r *Reader) namespace() string {
	if r.Namespace != "" {
		return r.Namespace
	}
	return DefaultNamespace
}

func (r *Reader) name() string {
	if r.Name != "" {
		return r.Name
	}
	return CloudProductName
}

// readLimit reads one top-level limit key of the cpconfig data. Only an exact
// "Unlimited" value leaves the resource unlimited; a missing key, a null value,
// a negative or non-numeric value and values of any other type all count as
// zero, which allows no use of that resource.
func readLimit(config map[string]any, key string) (int64, bool) {
	switch v := config[key].(type) {
	case string:
		if v == unlimitedValue {
			return 0, true
		}
		limit, err := strconv.ParseInt(v, 10, 64)
		if err != nil || limit < 0 {
			return 0, false
		}
		return limit, false
	case float64:
		if v < 0 {
			return 0, false
		}
		return int64(v), false
	case int64:
		if v < 0 {
			return 0, false
		}
		return v, false
	default:
		return 0, false
	}
}
