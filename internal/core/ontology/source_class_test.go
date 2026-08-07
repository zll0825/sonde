package ontology

import (
	"testing"

	"capital_observatory/pkg/model"
	pb "capital_observatory/pkg/proto/plugin/v1"
)

func TestSourceClassFromProto(t *testing.T) {
	for _, tc := range []struct {
		in   pb.SourceClass
		want model.SourceClass
	}{
		{pb.SourceClass_SOURCE_CLASS_REAL, model.SourceClassReal},
		{pb.SourceClass_SOURCE_CLASS_MOCK, model.SourceClassMock},
		{pb.SourceClass_SOURCE_CLASS_TEST, model.SourceClassTest},
		{pb.SourceClass_SOURCE_CLASS_UNSPECIFIED, model.SourceClassUnknown},
		{pb.SourceClass(99), model.SourceClassUnknown},
	} {
		if got := sourceClassFromProto(tc.in); got != tc.want {
			t.Errorf("sourceClassFromProto(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
