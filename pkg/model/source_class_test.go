package model

import "testing"

func TestNormalizeSourceClass(t *testing.T) {
	for _, tc := range []struct {
		in, want SourceClass
	}{
		{SourceClassReal, SourceClassReal},
		{SourceClassMock, SourceClassMock},
		{SourceClassTest, SourceClassTest},
		{SourceClassUnknown, SourceClassUnknown},
		{"", SourceClassUnknown},
		{"REAL", SourceClassUnknown},
		{"future", SourceClassUnknown},
	} {
		if got := NormalizeSourceClass(tc.in); got != tc.want {
			t.Errorf("NormalizeSourceClass(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
