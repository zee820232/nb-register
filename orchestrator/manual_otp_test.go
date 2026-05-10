package main

import (
	"testing"

	"orchestrator/db"
)

func TestManualOTPParamsForJob(t *testing.T) {
	cases := []struct {
		name      string
		job       db.Job
		wantParam string
		wantKind  string
		wantErr   bool
	}{
		{
			name:      "register",
			job:       db.Job{Action: actionRegister},
			wantParam: registrationOTPParam,
			wantKind:  "registration",
		},
		{
			name:      "activate",
			job:       db.Job{Action: actionActivate},
			wantParam: paymentOTPParam,
			wantKind:  "payment",
		},
		{
			name:      "register and activate during registration",
			job:       db.Job{Action: actionRegisterAndActivate, LastStep: stepRegisterAccount},
			wantParam: registrationOTPParam,
			wantKind:  "registration",
		},
		{
			name:      "register and activate during payment",
			job:       db.Job{Action: actionRegisterAndActivate, LastStep: stepGoPayPayment},
			wantParam: paymentOTPParam,
			wantKind:  "payment",
		},
		{
			name:    "unsupported",
			job:     db.Job{Action: actionProbePlusTrial},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			param, _, kind, err := manualOTPParamsForJobSnapshot(&tc.job)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if param != tc.wantParam || kind != tc.wantKind {
				t.Fatalf("param=%q kind=%q", param, kind)
			}
		})
	}
}
