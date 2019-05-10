/*
Copyright 2019 The Kubernetes Authors.

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

package schema

import (
	"reflect"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

var intOrStringAnyOf = []NestedValueValidation{
	{ForbiddenGenerics: Generic{
		Type: "integer",
	}},
	{ForbiddenGenerics: Generic{
		Type: "string",
	}},
}

type level int

const (
	rootLevel level = iota
	itemLevel
	fieldLevel
)

// ValidateStructural checks that s is a structural schema with the invariants:
//
// * structurality: both `ForbiddenGenerics` and `ForbiddenExtensions` only have zero values, with the two exceptions for IntOrString.
// * RawExtension: for every schema with `x-kubernetes-embedded-resource: true`, `x-kubernetes-preserve-unknown-fields: true` and `type: object` are set
// * IntOrString: for `x-kubernetes-int-or-string: true` either `type` is empty under `anyOf` and `allOf` or the schema structure is one of these:
//
// 	 1) anyOf:
//      - type: integer
//	    - type: string
//	 2) allOf:
//	    - anyOf:
//	      - type: integer
//	      - type: string
//	    - ... zero or more
//
// * every specified field or array in s is also specified outside of value validation.
// * additionalProperties at the root is not allowed.
func ValidateStructural(s *Structural, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateStructuralInvariants(s, rootLevel, fldPath)...)
	allErrs = append(allErrs, validateStructuralCompleteness(s, fldPath)...)

	return allErrs
}

// validateStructuralInvariants checks the invariants of a structural schema.
func validateStructuralInvariants(s *Structural, lvl level, fldPath *field.Path) field.ErrorList {
	if s == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateStructuralInvariants(s.Items, itemLevel, fldPath.Child("items"))...)
	for k, v := range s.Properties {
		allErrs = append(allErrs, validateStructuralInvariants(&v, fieldLevel, fldPath.Child("properties").Key(k))...)
	}
	allErrs = append(allErrs, validateGeneric(&s.Generic, lvl, fldPath)...)
	allErrs = append(allErrs, validateExtensions(&s.Extensions, fldPath)...)

	// detect the two IntOrString exceptions:
	// 1) anyOf:
	//    - type: integer
	//    - type: string
	// 2) allOf:
	//    - anyOf:
	//      - type: integer
	//      - type: string
	//    - ... zero or more
	skipAnyOf := false
	skipFirstAllOfAnyOf := false
	if s.XIntOrString && s.ValueValidation != nil {
		if len(s.ValueValidation.AnyOf) == 2 && reflect.DeepEqual(s.ValueValidation.AnyOf, intOrStringAnyOf) {
			skipAnyOf = true
		} else if len(s.ValueValidation.AllOf) >= 1 && len(s.ValueValidation.AllOf[0].AnyOf) == 2 && reflect.DeepEqual(s.ValueValidation.AllOf[0].AnyOf, intOrStringAnyOf) {
			skipFirstAllOfAnyOf = true
		}
	}

	allErrs = append(allErrs, validateValueValidation(s.ValueValidation, skipAnyOf, skipFirstAllOfAnyOf, fldPath)...)

	if s.XEmbeddedResource && s.Type != "object" {
		if len(s.Type) == 0 {
			allErrs = append(allErrs, field.Required(fldPath.Child("type"), "must be object if x-kubernetes-embedded-resource is true"))
		} else {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("type"), s.Type, "must be object if x-kubernetes-embedded-resource is true"))
		}
	} else if len(s.Type) == 0 && !s.Extensions.XIntOrString && !s.Extensions.XPreserveUnknownFields {
		switch lvl {
		case rootLevel:
			allErrs = append(allErrs, field.Required(fldPath.Child("type"), "must not be empty at the root"))
		case itemLevel:
			allErrs = append(allErrs, field.Required(fldPath.Child("type"), "must not be empty for specified array items"))
		case fieldLevel:
			allErrs = append(allErrs, field.Required(fldPath.Child("type"), "must not be empty for specified object fields"))
		}
	}

	if lvl == rootLevel && len(s.Type) > 0 && s.Type != "object" {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("type"), s.Type, "must be object at the root"))
	}

	if s.XEmbeddedResource && !s.XPreserveUnknownFields && s.Properties == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("properties"), "must not be empty if x-kubernetes-embedded-resource is true without x-kubernetes-preserve-unknown-fields"))
	}

	return allErrs
}

// validateGeneric checks the generic fields of a structural schema.
func validateGeneric(g *Generic, lvl level, fldPath *field.Path) field.ErrorList {
	if g == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if g.AdditionalProperties != nil {
		if lvl == rootLevel {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("additionalProperties"), "must not be used at the root"))
		}
		if g.AdditionalProperties.Structural != nil {
			allErrs = append(allErrs, validateStructuralInvariants(g.AdditionalProperties.Structural, fieldLevel, fldPath.Child("additionalProperties"))...)
		}
	}

	return allErrs
}

// validateExtensions checks Kubernetes vendor extensions of a structural schema.
func validateExtensions(x *Extensions, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if x.XIntOrString && x.XPreserveUnknownFields {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("x-kubernetes-preserve-unknown-fields"), x.XPreserveUnknownFields, "must be false if x-kubernetes-int-or-string is true"))
	}
	if x.XIntOrString && x.XEmbeddedResource {
		allErrs = append(allErrs, field.Invalid(fldPath.Child("x-kubernetes-embedded-resource"), x.XEmbeddedResource, "must be false if x-kubernetes-int-or-string is true"))
	}

	return allErrs
}

// validateValueValidation checks the value validation in a structural schema.
func validateValueValidation(v *ValueValidation, skipAnyOf, skipFirstAllOfAnyOf bool, fldPath *field.Path) field.ErrorList {
	if v == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	if !skipAnyOf {
		for i := range v.AnyOf {
			allErrs = append(allErrs, validateNestedValueValidation(&v.AnyOf[i], false, false, fldPath.Child("anyOf").Index(i))...)
		}
	}

	for i := range v.AllOf {
		skipAnyOf := false
		if skipFirstAllOfAnyOf && i == 0 {
			skipAnyOf = true
		}
		allErrs = append(allErrs, validateNestedValueValidation(&v.AllOf[i], skipAnyOf, false, fldPath.Child("allOf").Index(i))...)
	}

	for i := range v.OneOf {
		allErrs = append(allErrs, validateNestedValueValidation(&v.OneOf[i], false, false, fldPath.Child("oneOf").Index(i))...)
	}

	allErrs = append(allErrs, validateNestedValueValidation(v.Not, false, false, fldPath.Child("not"))...)

	return allErrs
}

// validateNestedValueValidation checks the nested value validation under a logic junctor in a structural schema.
func validateNestedValueValidation(v *NestedValueValidation, skipAnyOf, skipAllOfAnyOf bool, fldPath *field.Path) field.ErrorList {
	if v == nil {
		return nil
	}

	allErrs := field.ErrorList{}

	allErrs = append(allErrs, validateValueValidation(&v.ValueValidation, skipAnyOf, skipAllOfAnyOf, fldPath)...)
	allErrs = append(allErrs, validateNestedValueValidation(v.Items, false, false, fldPath.Child("items"))...)

	for k, fld := range v.Properties {
		allErrs = append(allErrs, validateNestedValueValidation(&fld, false, false, fldPath.Child("properties").Key(k))...)
	}

	if len(v.ForbiddenGenerics.Type) > 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("type"), "must be empty to be structural"))
	}
	if v.ForbiddenGenerics.AdditionalProperties != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("additionalProperties"), "must be undefined to be structural"))
	}
	if v.ForbiddenGenerics.Default.Object != nil {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("default"), "must be undefined to be structural"))
	}
	if len(v.ForbiddenGenerics.Title) > 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("title"), "must be empty to be structural"))
	}
	if len(v.ForbiddenGenerics.Description) > 0 {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("description"), "must be empty to be structural"))
	}
	if v.ForbiddenGenerics.Nullable {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("nullable"), "must be false to be structural"))
	}

	if v.ForbiddenExtensions.XPreserveUnknownFields {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("x-kubernetes-preserve-unknown-fields"), "must be false to be structural"))
	}
	if v.ForbiddenExtensions.XEmbeddedResource {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("x-kubernetes-embedded-resource"), "must be false to be structural"))
	}
	if v.ForbiddenExtensions.XIntOrString {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("x-kubernetes-int-or-string"), "must be false to be structural"))
	}

	return allErrs
}