package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"dashboard/pb"
)

type server struct {
	accountClient      pb.AccountDatabaseServiceClient
	orchestratorClient pb.OrchestratorServiceClient
	db                 *sql.DB
	staticDir          string
}

type jobRow struct {
	JobID        string    `json:"job_id"`
	AccountID    string    `json:"account_id"`
	Action       string    `json:"action"`
	Status       string    `json:"status"`
	Recoverable  bool      `json:"recoverable"`
	Retryable    bool      `json:"retryable"`
	LastStep     string    `json:"last_step"`
	ErrorMessage string    `json:"error_message"`
	ResultJSON   string    `json:"result_json"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Steps        []stepRow `json:"steps,omitempty"`
}

type stepRow struct {
	JobID        string    `json:"job_id,omitempty"`
	StepName     string    `json:"step_name"`
	Status       string    `json:"status"`
	Recoverable  bool      `json:"recoverable"`
	Retryable    bool      `json:"retryable"`
	ErrorMessage string    `json:"error_message"`
	ResultJSON   string    `json:"result_json"`
	StartedAt    int64     `json:"started_at"`
	CompletedAt  int64     `json:"completed_at"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type createAccountRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type updateAccountRequest struct {
	SessionToken string `json:"session_token"`
	AccessToken  string `json:"access_token"`
}

func main() {
	ctx := context.Background()

	accountConn, err := grpc.NewClient(envDefault("ACCOUNT_DB_ADDR", "account-db:50051"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("connect account-db: %v", err)
	}
	defer accountConn.Close()

	orchestratorConn, err := grpc.NewClient(envDefault("ORCHESTRATOR_ADDR", "orchestrator:50051"), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("connect orchestrator: %v", err)
	}
	defer orchestratorConn.Close()

	pg, err := sql.Open("pgx", envDefault("ORCHESTRATOR_PG_DSN", envDefault("PG_DSN", "")))
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	if err := pg.PingContext(ctx); err != nil {
		log.Fatalf("ping postgres: %v", err)
	}
	defer pg.Close()

	s := &server{
		accountClient:      pb.NewAccountDatabaseServiceClient(accountConn),
		orchestratorClient: pb.NewOrchestratorServiceClient(orchestratorConn),
		db:                 pg,
		staticDir:          envDefault("STATIC_DIR", "web/dist"),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/accounts", s.handleAccounts)
	mux.HandleFunc("/api/accounts/", s.handleAccount)
	mux.HandleFunc("/api/jobs", s.handleJobs)
	mux.HandleFunc("/api/jobs/", s.handleJob)
	mux.HandleFunc("/api/workflows/register", s.handleRegister)
	mux.HandleFunc("/api/workflows/activate", s.handleActivate)
	mux.HandleFunc("/api/workflows/register-and-activate", s.handleRegisterAndActivate)
	mux.HandleFunc("/", s.handleStatic)

	addr := envDefault("LISTEN_ADDR", ":8080")
	log.Printf("dashboard listening on %s", addr)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatal(err)
	}
}

func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := int32(queryInt(r, "limit", 100))
		resp, err := s.accountClient.ListAccounts(r.Context(), &pb.ListAccountsRequest{
			Status: r.URL.Query().Get("status"),
			Limit:  limit,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		accounts := resp.GetAccounts()
		if accounts == nil {
			accounts = []*pb.Account{}
		}
		writeJSON(w, http.StatusOK, accounts)
	case http.MethodPost:
		var req createAccountRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		resp, err := s.accountClient.CreateAccount(r.Context(), &pb.CreateAccountRequest{Account: &pb.Account{
			Email:    req.Email,
			Password: req.Password,
		}})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusCreated, resp.GetAccount())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *server) handleAccount(w http.ResponseWriter, r *http.Request) {
	accountID := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	if accountID == "" {
		writeError(w, http.StatusBadRequest, errors.New("account_id is required"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp, err := s.accountClient.GetAccount(r.Context(), &pb.GetAccountRequest{AccountId: accountID})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp.GetAccount())
	case http.MethodPatch, http.MethodPut:
		var req updateAccountRequest
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		sessionToken := strings.TrimSpace(req.SessionToken)
		accessToken := strings.TrimSpace(req.AccessToken)
		if sessionToken == "" && accessToken == "" {
			writeError(w, http.StatusBadRequest, errors.New("session_token or access_token is required"))
			return
		}
		resp, err := s.accountClient.UpdateAccount(r.Context(), &pb.UpdateAccountRequest{Account: &pb.Account{
			AccountId:    accountID,
			SessionToken: sessionToken,
			AccessToken:  accessToken,
		}})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp.GetAccount())
	case http.MethodDelete:
		resp, err := s.accountClient.DeleteAccount(r.Context(), &pb.DeleteAccountRequest{AccountId: accountID})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *server) handleJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	jobs, err := s.listJobs(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

func (s *server) handleJob(w http.ResponseWriter, r *http.Request) {
	jobID := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	jobID = strings.TrimSuffix(jobID, "/retry")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, errors.New("job_id is required"))
		return
	}

	if strings.HasSuffix(r.URL.Path, "/retry") {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.retryJob(w, r, jobID)
		return
	}

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	job, err := s.getJob(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *server) retryJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, err := s.getJob(r.Context(), jobID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if job.Status == "RUNNING" {
		writeError(w, http.StatusConflict, errors.New("running job cannot be retried"))
		return
	}
	if strings.TrimSpace(job.AccountID) == "" {
		writeError(w, http.StatusBadRequest, errors.New("job account_id is empty"))
		return
	}

	switch job.Action {
	case "REGISTER":
		resp, err := s.orchestratorClient.RegisterAccount(r.Context(), &pb.RegisterAccountRequest{AccountId: job.AccountID})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case "ACTIVATE":
		resp, err := s.orchestratorClient.ActivateAccount(r.Context(), &pb.ActivateAccountRequest{AccountId: job.AccountID})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	case "REGISTER_AND_ACTIVATE":
		resp, err := s.orchestratorClient.RegisterAndActivateAccount(r.Context(), &pb.RegisterAndActivateAccountRequest{AccountId: job.AccountID})
		if err != nil {
			writeError(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported job action: %s", job.Action))
	}
}

func (s *server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.RegisterAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.orchestratorClient.RegisterAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.ActivateAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.orchestratorClient.ActivateAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) handleRegisterAndActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req pb.RegisterAndActivateAccountRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	resp, err := s.orchestratorClient.RegisterAndActivateAccount(r.Context(), &req)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) listJobs(ctx context.Context, r *http.Request) ([]jobRow, error) {
	limit := queryInt(r, "limit", 100)
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	query := `SELECT id, account_id, action, status, recoverable, retryable, last_step, error_message, result_json, to_timestamp(created_at), to_timestamp(updated_at) FROM jobs WHERE 1=1`
	args := []any{}
	if value := strings.TrimSpace(r.URL.Query().Get("status")); value != "" {
		args = append(args, value)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if value := strings.TrimSpace(r.URL.Query().Get("action")); value != "" {
		args = append(args, value)
		query += fmt.Sprintf(" AND action = $%d", len(args))
	}
	if value := strings.TrimSpace(r.URL.Query().Get("account_id")); value != "" {
		args = append(args, value)
		query += fmt.Sprintf(" AND account_id = $%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY updated_at DESC LIMIT $%d", len(args))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	jobs := []jobRow{}
	for rows.Next() {
		var job jobRow
		if err := rows.Scan(&job.JobID, &job.AccountID, &job.Action, &job.Status, &job.Recoverable, &job.Retryable, &job.LastStep, &job.ErrorMessage, &job.ResultJSON, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *server) getJob(ctx context.Context, jobID string) (*jobRow, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, account_id, action, status, recoverable, retryable, last_step, error_message, result_json, to_timestamp(created_at), to_timestamp(updated_at) FROM jobs WHERE id = $1`, jobID)
	var job jobRow
	if err := row.Scan(&job.JobID, &job.AccountID, &job.Action, &job.Status, &job.Recoverable, &job.Retryable, &job.LastStep, &job.ErrorMessage, &job.ResultJSON, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT job_id, step_name, status, recoverable, retryable, error_message, result_json, started_at, completed_at, to_timestamp(created_at), to_timestamp(updated_at) FROM job_steps WHERE job_id = $1 ORDER BY started_at ASC, step_name ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var step stepRow
		if err := rows.Scan(&step.JobID, &step.StepName, &step.Status, &step.Recoverable, &step.Retryable, &step.ErrorMessage, &step.ResultJSON, &step.StartedAt, &step.CompletedAt, &step.CreatedAt, &step.UpdatedAt); err != nil {
			return nil, err
		}
		job.Steps = append(job.Steps, step)
	}
	return &job, rows.Err()
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := filepath.Join(s.staticDir, filepath.Clean(r.URL.Path))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		http.ServeFile(w, r, path)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.staticDir, "index.html"))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,PUT,DELETE,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func envDefault(key string, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}
