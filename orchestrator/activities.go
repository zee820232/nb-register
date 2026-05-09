package main

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"orchestrator/db"
	"orchestrator/pb"
)

func (s *orchestratorServer) CreateJobActivity(ctx context.Context, input CreateJobInput) error {
	_, err := s.createJobWithID(ctx, input.JobID, input.AccountID, input.Action, input.Params)
	return err
}

func (s *orchestratorServer) EnsureAccountActivity(ctx context.Context, input EnsureAccountInput) (AccountRef, error) {
	spec := input.Account
	if spec.AccountID == "" {
		return AccountRef{}, fmt.Errorf("account_id is required")
	}

	if account, err := s.getAccount(ctx, spec.AccountID); err == nil {
		return AccountRef{AccountID: account.GetAccountId()}, nil
	}

	resp, err := s.accountClient.CreateAccount(ctx, &pb.CreateAccountRequest{Account: &pb.Account{
		AccountId: spec.AccountID,
		Email:     spec.Email,
		Password:  spec.Password,
		Status:    statusCreated,
	}})
	if err != nil {
		if account, getErr := s.getAccount(ctx, spec.AccountID); getErr == nil {
			return AccountRef{AccountID: account.GetAccountId()}, nil
		}
		return AccountRef{}, err
	}
	if resp.GetAccount() == nil || resp.GetAccount().GetAccountId() == "" {
		return AccountRef{}, fmt.Errorf("account-db returned empty account")
	}
	return AccountRef{AccountID: resp.GetAccount().GetAccountId()}, nil
}

func (s *orchestratorServer) ResolveAccountFromJobActivity(ctx context.Context, input ResolveAccountInput) (AccountRef, error) {
	if input.AccountID != "" {
		account, err := s.getAccount(ctx, input.AccountID)
		if err != nil {
			return AccountRef{}, err
		}
		return AccountRef{AccountID: account.GetAccountId()}, nil
	}
	job, err := s.getJob(ctx, input.SourceJobID)
	if err != nil {
		return AccountRef{}, err
	}
	account, err := s.getAccount(ctx, job.AccountID)
	if err != nil {
		return AccountRef{}, err
	}
	return AccountRef{AccountID: account.GetAccountId()}, nil
}

func (s *orchestratorServer) RegisterAccountAtomicActivity(ctx context.Context, input RegisterActivityInput) (RegisterActivityOutput, error) {
	account, err := s.getAccount(ctx, input.AccountID)
	if err != nil {
		return RegisterActivityOutput{}, err
	}

	var result *pb.RegisterResponse
	var data map[string]any
	_, err = s.runAtomicStep(ctx, input.JobID, stepRegisterAccount, false, true, func() (any, error) {
		var stepErr error
		result, data, stepErr = s.register(ctx, input.JobID, account)
		return data, stepErr
	})
	if err != nil {
		return RegisterActivityOutput{Data: data}, err
	}
	return RegisterActivityOutput{
		SessionToken:      result.GetSessionToken(),
		AccessToken:       result.GetAccessToken(),
		DeviceID:          result.GetDeviceId(),
		PlusTrialEligible: result.GetPlusTrialEligible(),
		CheckoutURL:       result.GetCheckoutUrl(),
		Data:              data,
	}, nil
}

func (s *orchestratorServer) GoPayPaymentAtomicActivity(ctx context.Context, input GoPayActivityInput) (GoPayActivityOutput, error) {
	account, err := s.getAccount(ctx, input.AccountID)
	if err != nil {
		return GoPayActivityOutput{}, err
	}

	var result *pb.GoPayResponse
	var data map[string]any
	_, err = s.runAtomicStep(ctx, input.JobID, stepGoPayPayment, false, true, func() (any, error) {
		var stepErr error
		result, data, stepErr = s.pay(ctx, account, input.SessionToken, input.AccessToken)
		return data, stepErr
	})
	if err != nil {
		return GoPayActivityOutput{Data: data}, err
	}
	return GoPayActivityOutput{
		ChargeRef: result.GetChargeRef(),
		SnapToken: result.GetSnapToken(),
		Data:      data,
	}, nil
}

func (s *orchestratorServer) PersistRegisteredActivity(ctx context.Context, input PersistRegisteredInput) error {
	return s.updateAccount(ctx, &pb.Account{
		AccountId:    input.AccountID,
		Status:       "REGISTERED",
		SessionToken: input.SessionToken,
		AccessToken:  input.AccessToken,
	})
}

func (s *orchestratorServer) PersistActivatedActivity(ctx context.Context, input PersistActivatedInput) error {
	account, err := s.getAccount(ctx, input.AccountID)
	if err != nil {
		return err
	}
	sessionToken := input.SessionToken
	if sessionToken == "" {
		sessionToken = account.GetSessionToken()
	}
	accessToken := input.AccessToken
	if accessToken == "" {
		accessToken = account.GetAccessToken()
	}
	return s.updateAccount(ctx, &pb.Account{
		AccountId:    input.AccountID,
		Status:       "ACTIVATED",
		SessionToken: sessionToken,
		AccessToken:  accessToken,
		ChargeRef:    input.ChargeRef,
	})
}

func (s *orchestratorServer) MarkJobFailedActivity(ctx context.Context, input JobFailureInput) error {
	if input.Status == "" {
		input.Status = failedStatus(input.Recoverable, input.Retryable)
	}
	s.updateJob(ctx, input.JobID, input.Status, input.ErrorMessage, input.Result)
	if input.StepName != "" {
		return s.markStepFailed(ctx, input)
	}
	return nil
}

func (s *orchestratorServer) MarkJobSucceededActivity(ctx context.Context, input JobSuccessInput) error {
	s.updateJob(ctx, input.JobID, statusSucceeded, "", input.Result)
	return nil
}

func (s *orchestratorServer) createJobWithID(ctx context.Context, jobID, accountID, action string, params map[string]string) (*db.Job, error) {
	job := &db.Job{
		ID:        jobID,
		AccountID: accountID,
		Action:    action,
		Status:    statusCreated,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(job).Error; err != nil {
			return err
		}
		if err := upsertJobParams(ctx, tx, jobID, params); err != nil {
			return err
		}
		return tx.First(job, "id = ?", jobID).Error
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *orchestratorServer) markStepFailed(ctx context.Context, input JobFailureInput) error {
	now := time.Now().Unix()
	step := db.JobStep{
		JobID:        input.JobID,
		StepName:     input.StepName,
		Status:       input.Status,
		Recoverable:  input.Recoverable,
		Retryable:    input.Retryable,
		ErrorMessage: input.ErrorMessage,
		CompletedAt:  now,
	}
	updates := map[string]any{
		"status":        input.Status,
		"recoverable":   input.Recoverable,
		"retryable":     input.Retryable,
		"error_message": input.ErrorMessage,
		"completed_at":  now,
	}
	if len(input.Result) > 0 {
		updates["result_json"] = marshalStepResult(input.JobID, input.StepName, input.Result)
	}
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}, {Name: "step_name"}},
		DoUpdates: clause.Assignments(updates),
	}).Create(&step).Error
}
