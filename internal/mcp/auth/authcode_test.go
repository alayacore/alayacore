package auth

import "testing"

// TestValidateIssParam covers the RFC 9207 issuer validation rules.
func TestValidateIssParam(t *testing.T) {
	tests := []struct {
		name    string
		meta    *ASMetadata
		iss     string
		wantErr bool
	}{
		{
			name: "nil metadata passes",
			meta: nil,
			iss:  "",
		},
		{
			name: "absent iss and no advertised support passes (legacy)",
			meta: &ASMetadata{Issuer: "https://auth.example.com"},
			iss:  "",
		},
		{
			name: "matching iss passes",
			meta: &ASMetadata{Issuer: "https://auth.example.com"},
			iss:  "https://auth.example.com",
		},
		{
			name: "advertised support with absent iss is rejected",
			meta: &ASMetadata{
				Issuer:                            "https://auth.example.com",
				AuthorizationResponseIssParameter: boolPtr(true),
			},
			iss:     "",
			wantErr: true,
		},
		{
			name: "advertised support with matching iss passes",
			meta: &ASMetadata{
				Issuer:                            "https://auth.example.com",
				AuthorizationResponseIssParameter: boolPtr(true),
			},
			iss: "https://auth.example.com",
		},
		{
			name:    "mismatched iss is rejected",
			meta:    &ASMetadata{Issuer: "https://auth.example.com"},
			iss:     "https://evil.example.com",
			wantErr: true,
		},
		{
			name:    "no normalization before comparison (trailing slash)",
			meta:    &ASMetadata{Issuer: "https://auth.example.com"},
			iss:     "https://auth.example.com/",
			wantErr: true,
		},
		{
			name:    "no normalization before comparison (case)",
			meta:    &ASMetadata{Issuer: "https://auth.example.com"},
			iss:     "HTTPS://auth.example.com",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateIssParam(tt.meta, tt.iss)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateIssParam(iss=%q) error = %v, wantErr %v", tt.iss, err, tt.wantErr)
			}
		})
	}
}

func boolPtr(b bool) *bool { return &b }
