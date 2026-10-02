/*
Copyright 2026. projectsveltos.io. All rights reserved.

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

package controllers_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/projectsveltos/classifier/controllers"
	libsveltosv1beta1 "github.com/projectsveltos/libsveltos/api/v1beta1"
)

var _ = Describe("SortPatches", func() {
	It("sorts nil targets before non nil targets", func() {
		patches := []libsveltosv1beta1.Patch{
			{Patch: "with-target", Target: &libsveltosv1beta1.PatchSelector{Kind: "Deployment"}},
			{Patch: "no-target", Target: nil},
		}

		controllers.SortPatches(patches)
		Expect(patches[0].Patch).To(Equal("no-target"))
		Expect(patches[1].Patch).To(Equal("with-target"))
	})

	It("sorts by Group, then Version, then Kind, then Namespace, then Name", func() {
		patches := []libsveltosv1beta1.Patch{
			{Patch: "b", Target: &libsveltosv1beta1.PatchSelector{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "ns", Name: "b"}},
			{Patch: "a", Target: &libsveltosv1beta1.PatchSelector{Group: "apps", Version: "v1", Kind: "Deployment", Namespace: "ns", Name: "a"}},
			{Patch: "core", Target: &libsveltosv1beta1.PatchSelector{Group: "", Version: "v1", Kind: "Pod"}},
			{Patch: "batch", Target: &libsveltosv1beta1.PatchSelector{Group: "batch", Version: "v1", Kind: "Job"}},
		}

		controllers.SortPatches(patches)
		Expect(patches[0].Patch).To(Equal("core"))
		Expect(patches[1].Patch).To(Equal("a"))
		Expect(patches[2].Patch).To(Equal("b"))
		Expect(patches[3].Patch).To(Equal("batch"))
	})

	It("falls back to comparing the patch content when targets are identical", func() {
		target := &libsveltosv1beta1.PatchSelector{Group: "apps", Version: "v1", Kind: "Deployment"}
		patches := []libsveltosv1beta1.Patch{
			{Patch: "zzz", Target: target},
			{Patch: "aaa", Target: target},
		}

		controllers.SortPatches(patches)
		Expect(patches[0].Patch).To(Equal("aaa"))
		Expect(patches[1].Patch).To(Equal("zzz"))
	})

	It("is a no op on an empty or single element slice", func() {
		empty := []libsveltosv1beta1.Patch{}
		controllers.SortPatches(empty)
		Expect(empty).To(BeEmpty())

		single := []libsveltosv1beta1.Patch{{Patch: "only"}}
		controllers.SortPatches(single)
		Expect(single[0].Patch).To(Equal("only"))
	})
})
