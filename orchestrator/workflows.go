package main

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	taskQueueDefault = "nb-register-orchestrator"

	createJobActivityName         = "CreateJobActivity"
	ensureAccountActivityName     = "EnsureAccountActivity"
	resolveAccountActivityName    = "ResolveAccountFromJobActivity"
	registerAccountActivityName   = "RegisterAccountAtomicActivity"
	goPayPaymentActivityName      = "GoPayPaymentAtomicActivity"
	persistRegisteredActivityName = "PersistRegisteredActivity"
	persistActivatedActivityName  = "PersistActivatedActivity"
	markJobFailedActivityName     = "MarkJobFailedActivity"
	markJobSucceededActivityName  = "MarkJobSucceededActivity"
)

func RegisterAccountWorkflow(ctx workflow.Context, input RegisterAccountWorkflowInput) (RegisterAccountWorkflowResult, error) {
	result := RegisterAccountWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: input.Account.AccountID,
		Action:    actionRegister,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, ensureAccountActivityName, EnsureAccountInput{Account: input.Account}).Get(ctx, &account); err != nil {
		return failRegisterWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, nil), nil
	}

	var register RegisterActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, registerAccountActivityName, RegisterActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &register); err != nil {
		return failRegisterWorkflow(ctx, retryCtx, result, input.JobID, stepRegisterAccount, statusFailedRetryable, false, true, err, register.Data), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountID:    account.AccountID,
		SessionToken: register.SessionToken,
		AccessToken:  register.AccessToken,
	}).Get(ctx, nil); err != nil {
		return failRegisterWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, register.Data), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: register.Data,
	}).Get(ctx, nil)

	result.SessionToken = register.SessionToken
	result.AccessToken = register.AccessToken
	result.PlusTrialEligible = register.PlusTrialEligible
	result.CheckoutURL = register.CheckoutURL
	return result, nil
}

func ActivateAccountWorkflow(ctx workflow.Context, input ActivateAccountWorkflowInput) (ActivateAccountWorkflowResult, error) {
	result := ActivateAccountWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, resolveAccountActivityName, ResolveAccountInput{
		AccountID:   input.AccountID,
		SourceJobID: input.SourceJobID,
	}).Get(ctx, &account); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
		Action:    actionActivate,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var payment GoPayActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, goPayPaymentActivityName, GoPayActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &payment); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, stepGoPayPayment, statusFailedRetryable, false, true, err, payment.Data), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistActivatedActivityName, PersistActivatedInput{
		AccountID: account.AccountID,
		ChargeRef: payment.ChargeRef,
	}).Get(ctx, nil); err != nil {
		return failActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, payment.Data), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: payment.Data,
	}).Get(ctx, nil)

	result.Success = true
	result.ChargeRef = payment.ChargeRef
	result.SnapToken = payment.SnapToken
	return result, nil
}

func RegisterAndActivateWorkflow(ctx workflow.Context, input RegisterAndActivateWorkflowInput) (RegisterAndActivateWorkflowResult, error) {
	result := RegisterAndActivateWorkflowResult{JobID: input.JobID}
	retryCtx := workflow.WithActivityOptions(ctx, retryableActivityOptions(30*time.Second, 5))
	atomicCtx := workflow.WithActivityOptions(ctx, atomicActivityOptions(15*time.Minute))

	if err := workflow.ExecuteActivity(retryCtx, createJobActivityName, CreateJobInput{
		JobID:     input.JobID,
		AccountID: input.Account.AccountID,
		Action:    actionRegisterAndActivate,
	}).Get(ctx, nil); err != nil {
		result.ErrorMessage = err.Error()
		return result, nil
	}

	var account AccountRef
	if err := workflow.ExecuteActivity(retryCtx, ensureAccountActivityName, EnsureAccountInput{Account: input.Account}).Get(ctx, &account); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, nil), nil
	}

	var register RegisterActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, registerAccountActivityName, RegisterActivityInput{
		JobID:     input.JobID,
		AccountID: account.AccountID,
	}).Get(ctx, &register); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepRegisterAccount, statusFailedRetryable, false, true, err, register.Data), nil
	}

	if err := workflow.ExecuteActivity(retryCtx, persistRegisteredActivityName, PersistRegisteredInput{
		AccountID:    account.AccountID,
		SessionToken: register.SessionToken,
		AccessToken:  register.AccessToken,
	}).Get(ctx, nil); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, register.Data), nil
	}

	var payment GoPayActivityOutput
	if err := workflow.ExecuteActivity(atomicCtx, goPayPaymentActivityName, GoPayActivityInput{
		JobID:        input.JobID,
		AccountID:    account.AccountID,
		SessionToken: register.SessionToken,
		AccessToken:  register.AccessToken,
	}).Get(ctx, &payment); err != nil {
		combined := map[string]any{"register_account": register.Data, "gopay_payment": payment.Data}
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, stepGoPayPayment, statusFailedRetryable, false, true, err, combined), nil
	}

	combined := map[string]any{"register_account": register.Data, "gopay_payment": payment.Data}
	if err := workflow.ExecuteActivity(retryCtx, persistActivatedActivityName, PersistActivatedInput{
		AccountID:    account.AccountID,
		SessionToken: register.SessionToken,
		AccessToken:  register.AccessToken,
		ChargeRef:    payment.ChargeRef,
	}).Get(ctx, nil); err != nil {
		return failRegisterAndActivateWorkflow(ctx, retryCtx, result, input.JobID, "", statusFailedRecoverable, true, false, err, combined), nil
	}

	_ = workflow.ExecuteActivity(retryCtx, markJobSucceededActivityName, JobSuccessInput{
		JobID:  input.JobID,
		Result: combined,
	}).Get(ctx, nil)

	result.SessionToken = register.SessionToken
	result.AccessToken = register.AccessToken
	result.PlusTrialEligible = register.PlusTrialEligible
	result.CheckoutURL = register.CheckoutURL
	result.ActivationSuccess = true
	result.ChargeRef = payment.ChargeRef
	result.SnapToken = payment.SnapToken
	return result, nil
}

func failRegisterWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failActivateWorkflow(ctx workflow.Context, activityCtx workflow.Context, result ActivateAccountWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) ActivateAccountWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func failRegisterAndActivateWorkflow(ctx workflow.Context, activityCtx workflow.Context, result RegisterAndActivateWorkflowResult, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) RegisterAndActivateWorkflowResult {
	result.ErrorMessage = err.Error()
	markWorkflowFailure(ctx, activityCtx, jobID, stepName, status, recoverable, retryable, err, data)
	return result
}

func markWorkflowFailure(ctx workflow.Context, activityCtx workflow.Context, jobID, stepName, status string, recoverable, retryable bool, err error, data map[string]any) {
	_ = workflow.ExecuteActivity(activityCtx, markJobFailedActivityName, JobFailureInput{
		JobID:        jobID,
		StepName:     stepName,
		Status:       status,
		Recoverable:  recoverable,
		Retryable:    retryable,
		ErrorMessage: err.Error(),
		Result:       data,
	}).Get(ctx, nil)
}

func atomicActivityOptions(timeout time.Duration) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
}

func retryableActivityOptions(timeout time.Duration, attempts int32) workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    attempts,
		},
	}
}
