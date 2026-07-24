// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE.txt file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package hpa

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
)

func podsMetric(name string) autoscalingv2.MetricSpec {
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.PodsMetricSourceType,
		Pods: &autoscalingv2.PodsMetricSource{
			Metric: autoscalingv2.MetricIdentifier{Name: name},
		},
	}
}

func objectMetric(name string) autoscalingv2.MetricSpec {
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.ObjectMetricSourceType,
		Object: &autoscalingv2.ObjectMetricSource{
			Metric: autoscalingv2.MetricIdentifier{Name: name},
		},
	}
}

func resourceMetric() autoscalingv2.MetricSpec {
	return autoscalingv2.MetricSpec{
		Type:     autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricSource{Name: "cpu"},
	}
}

func externalMetric(name string) autoscalingv2.MetricSpec {
	return autoscalingv2.MetricSpec{
		Type: autoscalingv2.ExternalMetricSourceType,
		External: &autoscalingv2.ExternalMetricSource{
			Metric: autoscalingv2.MetricIdentifier{Name: name},
		},
	}
}

func hpaWith(specs ...autoscalingv2.MetricSpec) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{Metrics: specs},
	}
}

func TestMetricNames(t *testing.T) {
	tests := []struct {
		name string
		hpa  *autoscalingv2.HorizontalPodAutoscaler
		want []string
	}{
		{"nil", nil, nil},
		{"empty", hpaWith(), nil},
		{"pods", hpaWith(podsMetric("foo")), []string{"foo"}},
		{"object ignored", hpaWith(objectMetric("bar")), nil},
		{"pods kept, object ignored", hpaWith(podsMetric("foo"), objectMetric("bar")), []string{"foo"}},
		{"resource ignored", hpaWith(resourceMetric()), nil},
		{"external ignored", hpaWith(externalMetric("ext")), nil},
		{"mixed", hpaWith(podsMetric("foo"), resourceMetric(), externalMetric("ext"), objectMetric("bar")), []string{"foo"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.ElementsMatch(t, tc.want, metricNames(tc.hpa))
		})
	}
}

func sortStr(s []string) []string { sort.Strings(s); return s }

func TestReferenceTracker_AddFirstReference(t *testing.T) {
	tr := newReferenceTracker()

	added, removed := tr.upsert("ns/hpa1", []string{"foo", "bar"})
	assert.ElementsMatch(t, []string{"foo", "bar"}, added)
	assert.Empty(t, removed)
}

func TestReferenceTracker_SecondHPADoesNotReadvertise(t *testing.T) {
	tr := newReferenceTracker()
	tr.upsert("ns/hpa1", []string{"foo"})

	// A second HPA referencing the same metric must not re-trigger an advertise.
	added, removed := tr.upsert("ns/hpa2", []string{"foo"})
	assert.Empty(t, added)
	assert.Empty(t, removed)
}

func TestReferenceTracker_WithdrawOnlyWhenLastReferenceGone(t *testing.T) {
	tr := newReferenceTracker()
	tr.upsert("ns/hpa1", []string{"foo"})
	tr.upsert("ns/hpa2", []string{"foo"})

	// First HPA removed → still referenced by hpa2 → no withdraw.
	removed := tr.remove("ns/hpa1")
	assert.Empty(t, removed)

	// Second HPA removed → no references left → withdraw.
	removed = tr.remove("ns/hpa2")
	assert.ElementsMatch(t, []string{"foo"}, removed)
}

func TestReferenceTracker_UpsertDiff(t *testing.T) {
	tr := newReferenceTracker()
	tr.upsert("ns/hpa1", []string{"foo", "bar"})

	// HPA updated: drops "bar", adds "baz", keeps "foo".
	added, removed := tr.upsert("ns/hpa1", []string{"foo", "baz"})
	assert.ElementsMatch(t, []string{"baz"}, added)
	assert.ElementsMatch(t, []string{"bar"}, removed)
}

func TestReferenceTracker_UpsertToEmptyWithdrawsAll(t *testing.T) {
	tr := newReferenceTracker()
	tr.upsert("ns/hpa1", []string{"foo", "bar"})

	added, removed := tr.upsert("ns/hpa1", nil)
	assert.Empty(t, added)
	assert.Equal(t, []string{"bar", "foo"}, sortStr(removed))
}

func TestReferenceTracker_RemoveUnknownIsNoOp(t *testing.T) {
	tr := newReferenceTracker()
	assert.Empty(t, tr.remove("ns/does-not-exist"))
}

func TestReferenceTracker_SharedMetricAcrossDifferentMetrics(t *testing.T) {
	tr := newReferenceTracker()
	// hpa1 → foo, hpa2 → foo+bar
	tr.upsert("ns/hpa1", []string{"foo"})
	added, _ := tr.upsert("ns/hpa2", []string{"foo", "bar"})
	assert.ElementsMatch(t, []string{"bar"}, added, "only bar is newly referenced")

	// Remove hpa2 → bar withdrawn, foo still held by hpa1.
	removed := tr.remove("ns/hpa2")
	assert.ElementsMatch(t, []string{"bar"}, removed)
}
