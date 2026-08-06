package main

import "testing"

func TestBuildRegistrationUsesCanonicalRelationTaxonomy(t *testing.T) {
	registration := buildRegistration()
	if len(registration.GetRelations()) == 0 {
		t.Fatal("registration must declare macro relations")
	}
	for _, relation := range registration.GetRelations() {
		if got := relation.GetRelationType(); got != "causes" {
			t.Errorf("relation %s -> %s type = %q, want causes",
				relation.GetSourceId(), relation.GetTargetId(), got)
		}
	}
}
