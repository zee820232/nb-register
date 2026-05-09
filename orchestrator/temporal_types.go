package main

type AccountSpec struct {
	AccountID string
	Email     string
	Password  string
}

type CreateJobInput struct {
	JobID     string
	AccountID string
	Action    string
	Params    map[string]string
}

type EnsureAccountInput struct {
	Account AccountSpec
}

type AccountRef struct {
	AccountID string
}

type ResolveAccountInput struct {
	AccountID   string
	SourceJobID string
}

type RegisterActivityInput struct {
	JobID     string
	AccountID string
}

type RegisterActivityOutput struct {
	SessionToken      string
	AccessToken       string
	DeviceID          string
	PlusTrialEligible bool
	CheckoutURL       string
	Data              map[string]any
}

type GoPayActivityInput struct {
	JobID        string
	AccountID    string
	SessionToken string
	AccessToken  string
}

type GoPayActivityOutput struct {
	ChargeRef string
	SnapToken string
	Data      map[string]any
}

type PersistRegisteredInput struct {
	AccountID    string
	SessionToken string
	AccessToken  string
}

type PersistActivatedInput struct {
	AccountID    string
	SessionToken string
	AccessToken  string
	ChargeRef    string
}

type JobFailureInput struct {
	JobID        string
	StepName     string
	Status       string
	Recoverable  bool
	Retryable    bool
	ErrorMessage string
	Result       map[string]any
}

type JobSuccessInput struct {
	JobID  string
	Result map[string]any
}

type RegisterAccountWorkflowInput struct {
	JobID   string
	Account AccountSpec
}

type RegisterAccountWorkflowResult struct {
	JobID             string
	SessionToken      string
	AccessToken       string
	PlusTrialEligible bool
	CheckoutURL       string
	ErrorMessage      string
}

type ActivateAccountWorkflowInput struct {
	JobID       string
	AccountID   string
	SourceJobID string
}

type ActivateAccountWorkflowResult struct {
	JobID        string
	Success      bool
	ErrorMessage string
	ChargeRef    string
	SnapToken    string
}

type RegisterAndActivateWorkflowInput struct {
	JobID   string
	Account AccountSpec
}

type RegisterAndActivateWorkflowResult struct {
	JobID             string
	SessionToken      string
	AccessToken       string
	PlusTrialEligible bool
	CheckoutURL       string
	ActivationSuccess bool
	ErrorMessage      string
	ChargeRef         string
	SnapToken         string
}
