package event

import "testing"

func TestResearchRequestValidate(t *testing.T) {
	tests := []struct {
		name    string
		alertID string
		wantErr bool
	}{
		{name: "valid", alertID: "alt_001"},
		{name: "empty", wantErr: true},
		{name: "whitespace", alertID: "  ", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := NewResearchRequest(tt.alertID)
			if gotErr := req.Validate() != nil; gotErr != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", req.Validate(), tt.wantErr)
			}
		})
	}
}
