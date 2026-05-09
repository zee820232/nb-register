package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	temporalclient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	temporalworker "go.temporal.io/sdk/worker"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"orchestrator/db"
	"orchestrator/pb"
)

const (
	actionRegister            = "REGISTER"
	actionActivate            = "ACTIVATE"
	actionRegisterAndActivate = "REGISTER_AND_ACTIVATE"

	statusCreated           = "CREATED"
	statusRunning           = "RUNNING"
	statusSucceeded         = "SUCCEEDED"
	statusFailedRecoverable = "FAILED_RECOVERABLE"
	statusFailedRetryable   = "FAILED_RETRYABLE"
	statusFailedFinal       = "FAILED_FINAL"

	stepRegisterAccount = "register_account"
	stepGoPayPayment    = "gopay_payment"
)

type orchestratorServer struct {
	pb.UnimplementedOrchestratorServiceServer
	db            *gorm.DB
	accountClient pb.AccountDatabaseServiceClient
	browserClient pb.BrowserRegistrationClient
	paymentClient pb.PaymentServiceClient
	emailClient   pb.EmailServiceClient
	otpAddr       string
	otpTimeout    int32
	temporal      temporalclient.Client
	taskQueue     string
}

func (s *orchestratorServer) createAccount(ctx context.Context, account *pb.Account) (*pb.Account, error) {
	resp, err := s.accountClient.CreateAccount(ctx, &pb.CreateAccountRequest{Account: account})
	if err != nil {
		return nil, fmt.Errorf("create account: %w", err)
	}
	if resp.GetAccount() == nil || resp.GetAccount().GetAccountId() == "" {
		return nil, fmt.Errorf("account-db returned empty account")
	}
	return resp.GetAccount(), nil
}

func (s *orchestratorServer) getAccount(ctx context.Context, accountID string) (*pb.Account, error) {
	resp, err := s.accountClient.GetAccount(ctx, &pb.GetAccountRequest{AccountId: accountID})
	if err != nil {
		return nil, err
	}
	if resp.GetAccount() == nil {
		return nil, fmt.Errorf("account not found: %s", accountID)
	}
	return resp.GetAccount(), nil
}

func (s *orchestratorServer) updateAccount(ctx context.Context, account *pb.Account) error {
	_, err := s.accountClient.UpdateAccount(ctx, &pb.UpdateAccountRequest{Account: account})
	return err
}

func (s *orchestratorServer) createJob(ctx context.Context, accountID, action string, params map[string]string) (*db.Job, error) {
	job := &db.Job{
		ID:        uuid.NewString(),
		AccountID: accountID,
		Action:    action,
		Status:    statusCreated,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		return upsertJobParams(ctx, tx, job.ID, params)
	})
	if err != nil {
		return nil, err
	}
	return job, nil
}

func upsertJobParams(ctx context.Context, tx *gorm.DB, jobID string, params map[string]string) error {
	if len(params) == 0 {
		return nil
	}

	rows := make([]db.JobParam, 0, len(params))
	for key, value := range params {
		key = strings.TrimSpace(key)
		if key == "" || value == "" {
			continue
		}
		rows = append(rows, db.JobParam{JobID: jobID, Key: key, Value: value})
	}
	if len(rows) == 0 {
		return nil
	}

	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "job_id"}, {Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"}),
	}).Create(&rows).Error
}

func (s *orchestratorServer) updateJob(ctx context.Context, jobID, statusValue, errorMessage string, result any) {
	updates := map[string]any{
		"status":        statusValue,
		"recoverable":   statusValue == statusFailedRecoverable,
		"retryable":     statusValue == statusFailedRetryable,
		"error_message": errorMessage,
	}
	if result != nil {
		if b, err := json.Marshal(result); err == nil {
			updates["result_json"] = string(b)
		}
	}
	if err := s.db.WithContext(ctx).Model(&db.Job{}).Where("id = ?", jobID).Updates(updates).Error; err != nil {
		log.Printf("[orchestrator] update job failed job=%s: %v", jobID, err)
	}
}

func (s *orchestratorServer) getJob(ctx context.Context, jobID string) (*db.Job, error) {
	var job db.Job
	if err := s.db.WithContext(ctx).First(&job, "id = ?", jobID).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *orchestratorServer) runAtomicStep(ctx context.Context, jobID, stepName string, recoverable bool, retryable bool, fn func() (any, error)) (any, error) {
	startedAt := time.Now().Unix()
	start := db.JobStep{
		JobID:       jobID,
		StepName:    stepName,
		Status:      statusRunning,
		Recoverable: recoverable,
		Retryable:   retryable,
		StartedAt:   startedAt,
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "job_id"}, {Name: "step_name"}},
			DoUpdates: clause.Assignments(map[string]any{
				"status":        statusRunning,
				"recoverable":   recoverable,
				"retryable":     retryable,
				"error_message": "",
				"result_json":   "",
				"started_at":    startedAt,
				"completed_at":  int64(0),
			}),
		}).Create(&start).Error; err != nil {
			return err
		}
		return tx.Model(&db.Job{}).Where("id = ?", jobID).Updates(map[string]any{
			"status":        statusRunning,
			"recoverable":   false,
			"retryable":     false,
			"last_step":     stepName,
			"error_message": "",
		}).Error
	})
	if err != nil {
		return nil, err
	}

	result, stepErr := fn()
	completedAt := time.Now().Unix()
	resultJSON := marshalStepResult(jobID, stepName, result)
	statusValue := statusSucceeded
	errorMessage := ""
	if stepErr != nil {
		statusValue = failedStatus(recoverable, retryable)
		errorMessage = stepErr.Error()
	}

	updates := map[string]any{
		"status":        statusValue,
		"recoverable":   recoverable,
		"retryable":     retryable,
		"error_message": errorMessage,
		"result_json":   resultJSON,
		"completed_at":  completedAt,
	}
	if err := s.db.WithContext(ctx).Model(&db.JobStep{}).
		Where("job_id = ? AND step_name = ?", jobID, stepName).
		Updates(updates).Error; err != nil {
		log.Printf("[orchestrator] update step failed job=%s step=%s: %v", jobID, stepName, err)
	}

	if stepErr != nil {
		if err := s.db.WithContext(ctx).Model(&db.Job{}).Where("id = ?", jobID).Updates(map[string]any{
			"status":        statusValue,
			"recoverable":   recoverable,
			"retryable":     retryable,
			"last_step":     stepName,
			"error_message": errorMessage,
		}).Error; err != nil {
			log.Printf("[orchestrator] update failed job failed job=%s step=%s: %v", jobID, stepName, err)
		}
		return result, stepErr
	}

	return result, nil
}

func failedStatus(recoverable bool, retryable bool) string {
	if recoverable {
		return statusFailedRecoverable
	}
	if retryable {
		return statusFailedRetryable
	}
	return statusFailedFinal
}

func marshalStepResult(jobID, stepName string, result any) string {
	if result == nil {
		return ""
	}
	b, err := json.Marshal(result)
	if err != nil {
		log.Printf("[orchestrator] marshal step result failed job=%s step=%s: %v", jobID, stepName, err)
		return ""
	}
	return string(b)
}

func (s *orchestratorServer) register(ctx context.Context, jobID string, account *pb.Account) (result *pb.RegisterResponse, data map[string]any, err error) {
	data = map[string]any{
		"account_id": account.GetAccountId(),
		"email":      account.GetEmail(),
	}

	startResp, err := s.browserClient.StartRegister(ctx, &pb.RegisterRequest{
		JobId:         jobID,
		AssignedEmail: account.GetEmail(),
		Password:      account.GetPassword(),
		FirstName:     account.GetFirstName(),
		LastName:      account.GetLastName(),
		Birthday:      account.GetDob(),
	})
	data["browser_start"] = browserStartData(startResp)
	if err != nil {
		return nil, data, err
	}
	if startResp == nil {
		return nil, data, fmt.Errorf("browser start returned empty response")
	}
	if !startResp.GetSuccess() {
		return nil, data, fmt.Errorf("browser start failed: %s", startResp.GetErrorMessage())
	}

	flowID := startResp.GetFlowId()
	completed := false
	defer func() {
		if flowID != "" && !completed {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cancelResp, cancelErr := s.browserClient.CancelRegister(cancelCtx, &pb.CancelRegisterRequest{FlowId: flowID})
			data["cleanup"] = cleanupData(cancelResp.GetSuccess(), cancelResp.GetErrorMessage(), cancelErr)
		}
	}()

	if !startResp.GetOtpRequired() {
		result = startResp.GetResult()
		data["browser_complete"] = registerResultData(result)
		if result == nil {
			return nil, data, fmt.Errorf("browser completed without result")
		}
		if !result.GetSuccess() {
			return nil, data, fmt.Errorf("browser failed: %s", result.GetErrorMessage())
		}
		completed = true
		return result, data, nil
	}

	otpIssuedAfterUnix := startResp.GetOtpIssuedAfterUnix()
	otp, err := s.waitForRegistrationOtp(ctx, account.GetEmail(), 60, otpIssuedAfterUnix)
	data["registration_otp"] = map[string]any{
		"email":              account.GetEmail(),
		"timeout_seconds":    60,
		"issued_after_unix":  otpIssuedAfterUnix,
		"found":              err == nil,
		"otp_value_recorded": false,
	}
	if err != nil {
		return nil, data, err
	}

	result, err = s.browserClient.CompleteRegister(ctx, &pb.CompleteRegisterRequest{FlowId: flowID, Otp: otp})
	data["browser_complete"] = registerResultData(result)
	if err != nil {
		return nil, data, err
	}
	if result == nil {
		return nil, data, fmt.Errorf("browser complete returned empty response")
	}
	if !result.GetSuccess() {
		return nil, data, fmt.Errorf("browser complete failed: %s", result.GetErrorMessage())
	}
	completed = true
	return result, data, nil
}

func (s *orchestratorServer) waitForRegistrationOtp(ctx context.Context, email string, timeoutSeconds int32, issuedAfterUnix int64) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds+10)*time.Second)
	defer cancel()

	resp, err := s.emailClient.WaitForEmail(reqCtx, &pb.WaitForEmailRequest{
		EmailAddress:    email,
		SubjectKeyword:  "ChatGPT",
		TimeoutSeconds:  timeoutSeconds,
		IssuedAfterUnix: issuedAfterUnix,
	})
	if err != nil {
		return "", fmt.Errorf("registration otp not received after %ds: %w", timeoutSeconds, err)
	}
	if resp.GetFound() && resp.GetContentExtracted() != "" {
		return resp.GetContentExtracted(), nil
	}
	return "", fmt.Errorf("registration otp not received after %ds", timeoutSeconds)
}

func (s *orchestratorServer) pay(ctx context.Context, account *pb.Account, sessionToken, accessToken string) (result *pb.GoPayResponse, data map[string]any, err error) {
	if sessionToken == "" {
		sessionToken = account.GetSessionToken()
	}
	if accessToken == "" {
		accessToken = account.GetAccessToken()
	}
	data = map[string]any{
		"account_id":             account.GetAccountId(),
		"session_token_present":  sessionToken != "",
		"access_token_present":   accessToken != "",
		"otp_value_recorded":     false,
		"payment_result_present": false,
	}
	if sessionToken == "" && accessToken == "" {
		return nil, data, fmt.Errorf("session_token or access_token is required")
	}

	started, err := s.paymentClient.StartGoPay(ctx, &pb.StartGoPayRequest{
		SessionToken: sessionToken,
		AccessToken:  accessToken,
	})
	data["payment_start"] = paymentStartData(started)
	if err != nil {
		return nil, data, err
	}
	if started == nil {
		return nil, data, fmt.Errorf("payment start returned empty response")
	}
	if !started.GetSuccess() {
		return nil, data, fmt.Errorf("payment start failed: %s", started.GetErrorMessage())
	}

	flowID := started.GetFlowId()
	completed := false
	defer func() {
		if flowID != "" && !completed {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cancelResp, cancelErr := s.paymentClient.CancelGoPay(cancelCtx, &pb.CancelGoPayRequest{FlowId: flowID})
			data["cleanup"] = cleanupData(cancelResp.GetSuccess(), cancelResp.GetErrorMessage(), cancelErr)
		}
	}()

	otp, err := s.waitForPaymentOtp(ctx, started.GetIssuedAfterUnix())
	data["payment_otp"] = map[string]any{
		"timeout_seconds":    s.paymentOtpTimeout(),
		"issued_after_unix":  started.GetIssuedAfterUnix(),
		"found":              err == nil,
		"otp_value_recorded": false,
	}
	if err != nil {
		return nil, data, err
	}

	result, err = s.paymentClient.CompleteGoPay(ctx, &pb.CompleteGoPayRequest{FlowId: flowID, Otp: otp})
	data["payment_complete"] = paymentResultData(result)
	data["payment_result_present"] = result != nil
	if err != nil {
		return nil, data, err
	}
	if result == nil {
		return nil, data, fmt.Errorf("payment complete returned empty response")
	}
	if !result.GetSuccess() {
		return nil, data, fmt.Errorf("payment complete failed: %s", result.GetErrorMessage())
	}
	completed = true
	return result, data, nil
}

func (s *orchestratorServer) waitForPaymentOtp(ctx context.Context, issuedAfterUnix int64) (string, error) {
	addr := s.otpAddr
	if addr == "" {
		addr = "gopay-payment:50051"
	}
	timeoutSeconds := s.paymentOtpTimeout()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return "", err
	}
	defer conn.Close()

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds+10)*time.Second)
	defer cancel()

	resp, err := pb.NewOtpServiceClient(conn).WaitForOtp(reqCtx, &pb.WaitForOtpRequest{
		Purpose:         "gopay",
		TimeoutSeconds:  timeoutSeconds,
		IssuedAfterUnix: issuedAfterUnix,
	})
	if err != nil {
		return "", fmt.Errorf("otp not received after %ds: %w", timeoutSeconds, err)
	}
	if resp.GetFound() && resp.GetOtp() != "" {
		return resp.GetOtp(), nil
	}
	lastErr := resp.GetErrorMessage()
	if lastErr == "" {
		lastErr = "otp not found"
	}
	return "", fmt.Errorf("otp not received after %ds: %s", timeoutSeconds, lastErr)
}

func (s *orchestratorServer) RegisterAccount(ctx context.Context, req *pb.RegisterAccountRequest) (*pb.RegisterAccountResponse, error) {
	jobID := uuid.NewString()
	accountID := strings.TrimSpace(req.GetAccountId())
	if accountID == "" {
		accountID = uuid.NewString()
	}
	var result RegisterAccountWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions("register-"+jobID), RegisterAccountWorkflow, RegisterAccountWorkflowInput{
		JobID: jobID,
		Account: AccountSpec{
			AccountID: accountID,
			Email:     req.GetEmail(),
			Password:  req.GetPassword(),
		},
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.RegisterAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	return &pb.RegisterAccountResponse{
		JobId:             result.JobID,
		SessionToken:      result.SessionToken,
		AccessToken:       result.AccessToken,
		PlusTrialEligible: result.PlusTrialEligible,
		ErrorMessage:      result.ErrorMessage,
		CheckoutUrl:       result.CheckoutURL,
	}, nil
}

func (s *orchestratorServer) ActivateAccount(ctx context.Context, req *pb.ActivateAccountRequest) (*pb.ActivateAccountResponse, error) {
	jobID := uuid.NewString()
	var result ActivateAccountWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions("activate-"+jobID), ActivateAccountWorkflow, ActivateAccountWorkflowInput{
		JobID:       jobID,
		AccountID:   strings.TrimSpace(req.GetAccountId()),
		SourceJobID: req.GetJobId(),
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.ActivateAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	return &pb.ActivateAccountResponse{
		JobId:        result.JobID,
		Success:      result.Success,
		ErrorMessage: result.ErrorMessage,
		ChargeRef:    result.ChargeRef,
		SnapToken:    result.SnapToken,
	}, nil
}

func (s *orchestratorServer) RegisterAndActivateAccount(ctx context.Context, req *pb.RegisterAndActivateAccountRequest) (*pb.RegisterAndActivateAccountResponse, error) {
	jobID := uuid.NewString()
	accountID := strings.TrimSpace(req.GetAccountId())
	if accountID == "" {
		accountID = uuid.NewString()
	}
	var result RegisterAndActivateWorkflowResult
	run, err := s.temporal.ExecuteWorkflow(ctx, s.workflowOptions("register-activate-"+jobID), RegisterAndActivateWorkflow, RegisterAndActivateWorkflowInput{
		JobID: jobID,
		Account: AccountSpec{
			AccountID: accountID,
			Email:     req.GetEmail(),
			Password:  req.GetPassword(),
		},
	})
	if err != nil {
		return nil, err
	}
	if err := run.Get(ctx, &result); err != nil {
		return &pb.RegisterAndActivateAccountResponse{JobId: jobID, ErrorMessage: err.Error()}, nil
	}

	return &pb.RegisterAndActivateAccountResponse{
		JobId:             result.JobID,
		SessionToken:      result.SessionToken,
		AccessToken:       result.AccessToken,
		PlusTrialEligible: result.PlusTrialEligible,
		CheckoutUrl:       result.CheckoutURL,
		ActivationSuccess: result.ActivationSuccess,
		ErrorMessage:      result.ErrorMessage,
		ChargeRef:         result.ChargeRef,
		SnapToken:         result.SnapToken,
	}, nil
}

func (s *orchestratorServer) GetJob(ctx context.Context, req *pb.GetJobRequest) (*pb.GetJobResponse, error) {
	jobID := strings.TrimSpace(req.GetJobId())
	if jobID == "" {
		return &pb.GetJobResponse{ErrorMessage: "job_id is required"}, nil
	}

	job, err := s.getJob(ctx, jobID)
	if err != nil {
		return &pb.GetJobResponse{ErrorMessage: err.Error()}, nil
	}

	var steps []db.JobStep
	if err := s.db.WithContext(ctx).Where("job_id = ?", jobID).Order("started_at ASC, step_name ASC").Find(&steps).Error; err != nil {
		return &pb.GetJobResponse{ErrorMessage: err.Error()}, nil
	}

	return &pb.GetJobResponse{Job: jobToProto(job, steps)}, nil
}

func (s *orchestratorServer) RequestAccount(ctx context.Context, req *pb.RequestAccountRequest) (*pb.RequestAccountResponse, error) {
	resp, err := s.RegisterAccount(ctx, &pb.RegisterAccountRequest{})
	if err != nil {
		return nil, err
	}

	return &pb.RequestAccountResponse{
		JobId:             resp.JobId,
		SessionToken:      resp.SessionToken,
		AccessToken:       resp.AccessToken,
		PlusTrialEligible: resp.PlusTrialEligible,
		ErrorMessage:      resp.ErrorMessage,
		CheckoutUrl:       resp.CheckoutUrl,
	}, nil
}

func jobToProto(job *db.Job, steps []db.JobStep) *pb.Job {
	if job == nil {
		return nil
	}
	out := &pb.Job{
		JobId:        job.ID,
		AccountId:    job.AccountID,
		Action:       job.Action,
		Status:       job.Status,
		Recoverable:  job.Recoverable,
		Retryable:    job.Retryable,
		LastStep:     job.LastStep,
		ErrorMessage: job.ErrorMessage,
		ResultJson:   job.ResultJSON,
		CreatedAt:    job.CreatedAt,
		UpdatedAt:    job.UpdatedAt,
		Steps:        make([]*pb.JobStep, 0, len(steps)),
	}
	for i := range steps {
		out.Steps = append(out.Steps, &pb.JobStep{
			StepName:     steps[i].StepName,
			Status:       steps[i].Status,
			Recoverable:  steps[i].Recoverable,
			Retryable:    steps[i].Retryable,
			ErrorMessage: steps[i].ErrorMessage,
			ResultJson:   steps[i].ResultJSON,
			StartedAt:    steps[i].StartedAt,
			CompletedAt:  steps[i].CompletedAt,
		})
	}
	return out
}

func browserStartData(resp *pb.StartRegisterResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present": true,
		"success":          resp.GetSuccess(),
		"error_message":    resp.GetErrorMessage(),
		"flow_id":          resp.GetFlowId(),
		"otp_required":     resp.GetOtpRequired(),
		"otp_issued_after": resp.GetOtpIssuedAfterUnix(),
		"result":           registerResultData(resp.GetResult()),
	}
}

func registerResultData(resp *pb.RegisterResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":         true,
		"success":                  resp.GetSuccess(),
		"error_message":            resp.GetErrorMessage(),
		"session_token_present":    resp.GetSessionToken() != "",
		"access_token_present":     resp.GetAccessToken() != "",
		"device_id_present":        resp.GetDeviceId() != "",
		"plus_trial_eligible":      resp.GetPlusTrialEligible(),
		"checkout_url_present":     resp.GetCheckoutUrl() != "",
		"sensitive_values_stored":  false,
		"credential_values_stored": false,
	}
}

func paymentStartData(resp *pb.StartGoPayResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":   true,
		"success":            resp.GetSuccess(),
		"error_message":      resp.GetErrorMessage(),
		"flow_id":            resp.GetFlowId(),
		"snap_token_present": resp.GetSnapToken() != "",
		"issued_after_unix":  resp.GetIssuedAfterUnix(),
		"expires_at_unix":    resp.GetExpiresAtUnix(),
	}
}

func paymentResultData(resp *pb.GoPayResponse) map[string]any {
	if resp == nil {
		return map[string]any{"response_present": false}
	}
	return map[string]any{
		"response_present":   true,
		"success":            resp.GetSuccess(),
		"error_message":      resp.GetErrorMessage(),
		"charge_ref":         resp.GetChargeRef(),
		"snap_token_present": resp.GetSnapToken() != "",
	}
}

func cleanupData(success bool, errorMessage string, err error) map[string]any {
	data := map[string]any{
		"called":        true,
		"success":       success,
		"error_message": errorMessage,
	}
	if err != nil {
		data["rpc_error"] = err.Error()
	}
	return data
}

func (s *orchestratorServer) paymentOtpTimeout() int32 {
	if s.otpTimeout <= 0 {
		return 60
	}
	return s.otpTimeout
}

func main() {
	log.Println("Initializing Orchestrator API...")

	browserAddr := envDefault("BROWSER_ADDR", "browser-reg:50051")
	browserConn, err := grpc.NewClient(browserAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to browser service: %v", err)
	}
	defer browserConn.Close()

	paymentAddr := envDefault("PAYMENT_ADDR", "host.docker.internal:50051")
	paymentConn, err := grpc.NewClient(paymentAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to payment service: %v", err)
	}
	defer paymentConn.Close()

	accountDBAddr := envDefault("ACCOUNT_DB_ADDR", "account-db:50051")
	accountDBConn, err := grpc.NewClient(accountDBAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to account-db service: %v", err)
	}
	defer accountDBConn.Close()

	emailAddr := envDefault("EMAIL_ADDR", "outlook-imap-service:50051")
	emailConn, err := grpc.NewClient(emailAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect to email service: %v", err)
	}
	defer emailConn.Close()

	temporalNamespace := envDefault("TEMPORAL_NAMESPACE", "default")
	temporalClient, closeTemporal, err := newTemporalClient(temporalNamespace)
	if err != nil {
		log.Fatalf("Failed to connect to Temporal: %v", err)
	}
	defer closeTemporal()

	listenAddr := envDefault("LISTEN_ADDR", ":50051")
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server := &orchestratorServer{
		db:            db.InitDB(),
		accountClient: pb.NewAccountDatabaseServiceClient(accountDBConn),
		browserClient: pb.NewBrowserRegistrationClient(browserConn),
		paymentClient: pb.NewPaymentServiceClient(paymentConn),
		emailClient:   pb.NewEmailServiceClient(emailConn),
		otpAddr:       envDefault("GOPAY_OTP_SERVICE_ADDR", envDefault("OTP_ADDR", "gopay-payment:50051")),
		otpTimeout:    envInt32("GOPAY_OTP_TIMEOUT_SECONDS", 60),
		temporal:      temporalClient,
		taskQueue:     envDefault("TEMPORAL_TASK_QUEUE", taskQueueDefault),
	}

	temporalWorker := temporalworker.New(temporalClient, server.taskQueue, temporalworker.Options{})
	registerTemporalWorker(temporalWorker, server)
	go func() {
		if err := temporalWorker.Run(temporalworker.InterruptCh()); err != nil {
			log.Fatalf("Temporal worker failed: %v", err)
		}
	}()

	grpcServer := grpc.NewServer()
	pb.RegisterOrchestratorServiceServer(grpcServer, server)

	log.Printf("Orchestrator gRPC API listening on %s", listenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

func (s *orchestratorServer) workflowOptions(workflowID string) temporalclient.StartWorkflowOptions {
	return temporalclient.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: s.taskQueue,
	}
}

func newTemporalClient(namespace string) (temporalclient.Client, func(), error) {
	if envBool("TEMPORAL_DEV_SERVER", false) {
		options := testsuite.DevServerOptions{
			CachedDownload: testsuite.CachedDownload{
				Version: envDefault("TEMPORAL_DEV_SERVER_VERSION", "default"),
				DestDir: strings.TrimSpace(os.Getenv("TEMPORAL_DEV_SERVER_CACHE_DIR")),
			},
			ClientOptions: &temporalclient.Options{
				Namespace: namespace,
			},
			DBFilename: strings.TrimSpace(os.Getenv("TEMPORAL_DEV_SERVER_DB")),
			EnableUI:   envBool("TEMPORAL_DEV_SERVER_UI", false),
			UIPort:     strings.TrimSpace(os.Getenv("TEMPORAL_DEV_SERVER_UI_PORT")),
			LogLevel:   envDefault("TEMPORAL_DEV_SERVER_LOG_LEVEL", "warn"),
		}
		server, err := testsuite.StartDevServer(context.Background(), options)
		if err != nil {
			return nil, nil, err
		}
		log.Printf("Temporal dev server listening on %s", server.FrontendHostPort())
		client := server.Client()
		return client, func() {
			client.Close()
			if err := server.Stop(); err != nil {
				log.Printf("Temporal dev server stop failed: %v", err)
			}
		}, nil
	}

	temporalAddr := envDefault("TEMPORAL_ADDR", "host.docker.internal:7233")
	client, err := temporalclient.Dial(temporalclient.Options{
		HostPort:  temporalAddr,
		Namespace: namespace,
	})
	if err != nil {
		return nil, nil, err
	}
	return client, client.Close, nil
}

func envDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envInt32(key string, fallback int32) int32 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return int32(n)
}

func envBool(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
