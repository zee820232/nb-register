package activities

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"orchestrator/pb"
)

type accountClientForEmailStatusTest struct {
	account *pb.Account
}

func (c *accountClientForEmailStatusTest) CreateAccount(context.Context, *pb.CreateAccountRequest, ...grpc.CallOption) (*pb.CreateAccountResponse, error) {
	return nil, nil
}

func (c *accountClientForEmailStatusTest) GetAccount(context.Context, *pb.GetAccountRequest, ...grpc.CallOption) (*pb.GetAccountResponse, error) {
	return &pb.GetAccountResponse{Account: c.account}, nil
}

func (c *accountClientForEmailStatusTest) UpdateAccount(context.Context, *pb.UpdateAccountRequest, ...grpc.CallOption) (*pb.UpdateAccountResponse, error) {
	return &pb.UpdateAccountResponse{Account: c.account}, nil
}

func (c *accountClientForEmailStatusTest) DeleteAccount(context.Context, *pb.DeleteAccountRequest, ...grpc.CallOption) (*pb.DeleteAccountResponse, error) {
	return nil, nil
}

func (c *accountClientForEmailStatusTest) ListAccounts(context.Context, *pb.ListAccountsRequest, ...grpc.CallOption) (*pb.ListAccountsResponse, error) {
	return nil, nil
}

type emailClientForEmailStatusTest struct {
	markStatus *pb.MarkEmailStatusRequest
}

func (c *emailClientForEmailStatusTest) GetEmail(context.Context, *pb.GetEmailRequest, ...grpc.CallOption) (*pb.GetEmailResponse, error) {
	return nil, nil
}

func (c *emailClientForEmailStatusTest) MarkEmailStatus(_ context.Context, req *pb.MarkEmailStatusRequest, _ ...grpc.CallOption) (*pb.MarkEmailStatusResponse, error) {
	c.markStatus = req
	return &pb.MarkEmailStatusResponse{Mailbox: &pb.EmailMailbox{
		EmailAddress: req.GetEmailAddress(),
		Status:       req.GetStatus(),
	}}, nil
}

func (c *emailClientForEmailStatusTest) MarkEmailAuthStatus(context.Context, *pb.MarkEmailAuthStatusRequest, ...grpc.CallOption) (*pb.MarkEmailAuthStatusResponse, error) {
	return nil, nil
}

func (c *emailClientForEmailStatusTest) UpsertMailbox(context.Context, *pb.UpsertEmailMailboxRequest, ...grpc.CallOption) (*pb.UpsertEmailMailboxResponse, error) {
	return nil, nil
}

func (c *emailClientForEmailStatusTest) ListMailboxes(context.Context, *pb.ListEmailMailboxesRequest, ...grpc.CallOption) (*pb.ListEmailMailboxesResponse, error) {
	return nil, nil
}

func (c *emailClientForEmailStatusTest) DeleteMailbox(context.Context, *pb.DeleteMailboxRequest, ...grpc.CallOption) (*pb.DeleteMailboxResponse, error) {
	return nil, nil
}

func (c *emailClientForEmailStatusTest) WaitForEmail(context.Context, *pb.WaitForEmailRequest, ...grpc.CallOption) (*pb.WaitForEmailResponse, error) {
	return nil, nil
}

func (c *emailClientForEmailStatusTest) FetchInboxes(context.Context, *pb.FetchInboxesRequest, ...grpc.CallOption) (*pb.FetchInboxesResponse, error) {
	return nil, nil
}

func TestMarkAccountEmailUserAlreadyExists(t *testing.T) {
	emailClient := &emailClientForEmailStatusTest{}
	server := &Server{
		accountClient: &accountClientForEmailStatusTest{
			account: &pb.Account{
				AccountId: "account-1",
				Email:     "ConnieKaiser5272@outlook.com",
			},
		},
		emailClient: emailClient,
	}

	if err := server.markAccountEmailUserAlreadyExists(context.Background(), "account-1", "user already exists"); err != nil {
		t.Fatalf("mark account email user already exists: %v", err)
	}

	req := emailClient.markStatus
	if req == nil {
		t.Fatal("expected MarkEmailStatus call")
	}
	if req.GetEmailAddress() != "ConnieKaiser5272@outlook.com" {
		t.Fatalf("email address = %q, want assigned account email", req.GetEmailAddress())
	}
	if req.GetStatus() != emailStatusUserAlreadyExists {
		t.Fatalf("status = %q, want %q", req.GetStatus(), emailStatusUserAlreadyExists)
	}
	if req.GetLastError() != "user already exists" {
		t.Fatalf("last error = %q", req.GetLastError())
	}
}
