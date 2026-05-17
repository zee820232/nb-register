package activities

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"orchestrator/pb"
)

type accountClientForEmailStatusTest struct {
	account    *pb.Account
	markStatus *pb.MarkGPTEmailAllocationStatusRequest
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

func (c *accountClientForEmailStatusTest) UpsertGPTEmailAllocation(context.Context, *pb.UpsertGPTEmailAllocationRequest, ...grpc.CallOption) (*pb.UpsertGPTEmailAllocationResponse, error) {
	return nil, nil
}

func (c *accountClientForEmailStatusTest) ListGPTEmailAllocations(context.Context, *pb.ListGPTEmailAllocationsRequest, ...grpc.CallOption) (*pb.ListGPTEmailAllocationsResponse, error) {
	return nil, nil
}

func (c *accountClientForEmailStatusTest) ClaimGPTEmailAllocation(context.Context, *pb.ClaimGPTEmailAllocationRequest, ...grpc.CallOption) (*pb.ClaimGPTEmailAllocationResponse, error) {
	return nil, nil
}

func (c *accountClientForEmailStatusTest) CreateGPTEmailAliasAllocation(context.Context, *pb.CreateGPTEmailAliasAllocationRequest, ...grpc.CallOption) (*pb.CreateGPTEmailAliasAllocationResponse, error) {
	return nil, nil
}

func (c *accountClientForEmailStatusTest) MarkGPTEmailAllocationStatus(_ context.Context, req *pb.MarkGPTEmailAllocationStatusRequest, _ ...grpc.CallOption) (*pb.MarkGPTEmailAllocationStatusResponse, error) {
	c.markStatus = req
	return &pb.MarkGPTEmailAllocationStatusResponse{Allocation: &pb.GPTEmailAllocation{
		Email:  req.GetEmail(),
		Status: req.GetStatus(),
	}}, nil
}

func TestMarkAccountEmailUserAlreadyExists(t *testing.T) {
	accountClient := &accountClientForEmailStatusTest{
		account: &pb.Account{
			AccountId: "account-1",
			Email:     "ConnieKaiser5272@outlook.com",
		},
	}
	server := &Server{
		accountClient: accountClient,
	}

	if err := server.markAccountEmailUserAlreadyExists(context.Background(), "account-1", "user already exists"); err != nil {
		t.Fatalf("mark account email user already exists: %v", err)
	}

	req := accountClient.markStatus
	if req == nil {
		t.Fatal("expected MarkGPTEmailAllocationStatus call")
	}
	if req.GetEmail() != "ConnieKaiser5272@outlook.com" {
		t.Fatalf("email address = %q, want assigned account email", req.GetEmail())
	}
	if req.GetStatus() != emailStatusUserAlreadyExists {
		t.Fatalf("status = %q, want %q", req.GetStatus(), emailStatusUserAlreadyExists)
	}
	if req.GetLastError() != "user already exists" {
		t.Fatalf("last error = %q", req.GetLastError())
	}
}
