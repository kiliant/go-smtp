package smtpserver

import (
	"context"
	"errors"
	"testing"
)

func TestFinalizeAuthenticationCommitsSuccessfulMechanismShapesExactlyOnce(t *testing.T) {
	for _, mechanism := range []string{"PLAIN", "LOGIN", "EXTERNAL", "CRAM-MD5", "SCRAM-SHA-256"} {
		t.Run(mechanism, func(t *testing.T) {
			commits := 0
			session := &Session{CommitAuth: func(context.Context, *AuthResult, *CommitAuthOptions) { commits++ }}
			result := &AuthResult{Identity: "user@example.test"}
			got, err := finalizeAuthentication(context.Background(), session, result, nil, true)
			if err != nil || got != result || commits != 1 {
				t.Fatalf("finalize = (%p, %v), commits = %d", got, err, commits)
			}
		})
	}
}

func TestFinalizeAuthenticationNeverCommitsNonSuccess(t *testing.T) {
	wantErr := errors.New("identity store unavailable")
	tests := []struct {
		name             string
		result           *AuthResult
		verificationErr  error
		exchangeComplete bool
	}{
		{name: "refusal", result: &AuthResult{Failure: &AuthFailure{}}, exchangeComplete: true},
		{name: "abort before final round trip", result: &AuthResult{Identity: "user@example.test"}},
		{name: "internal failure", verificationErr: wantErr, exchangeComplete: true},
		{name: "nil outcome", exchangeComplete: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			commits := 0
			session := &Session{CommitAuth: func(context.Context, *AuthResult, *CommitAuthOptions) { commits++ }}
			_, _ = finalizeAuthentication(context.Background(), session, test.result, test.verificationErr, test.exchangeComplete)
			if commits != 0 {
				t.Fatalf("CommitAuth calls = %d", commits)
			}
		})
	}
}

func TestSCRAMCredentialLookupDoesNotCommit(t *testing.T) {
	commits := 0
	lookups := 0
	session := &Session{
		SCRAMCredentials: func(context.Context, *Credentials, *SCRAMOptions) (*SCRAMKeys, error) {
			lookups++
			return &SCRAMKeys{Result: AuthResult{Identity: "user@example.test"}}, nil
		},
		CommitAuth: func(context.Context, *AuthResult, *CommitAuthOptions) { commits++ },
	}
	keys, err := session.SCRAMCredentials(context.Background(), &Credentials{
		Mechanism:        "SCRAM-SHA-256",
		AuthenticationID: "user@example.test",
	}, nil)
	if err != nil || lookups != 1 || commits != 0 {
		t.Fatalf("lookup = (%+v, %v), lookups = %d, commits = %d", keys, err, lookups, commits)
	}
	if _, err := finalizeAuthentication(context.Background(), session, &keys.Result, nil, false); err != nil {
		t.Fatal(err)
	}
	if commits != 0 {
		t.Fatalf("CommitAuth ran before proof completion: %d", commits)
	}
	if _, err := finalizeAuthentication(context.Background(), session, &keys.Result, nil, true); err != nil {
		t.Fatal(err)
	}
	if commits != 1 {
		t.Fatalf("CommitAuth calls after proof = %d, want 1", commits)
	}
}
