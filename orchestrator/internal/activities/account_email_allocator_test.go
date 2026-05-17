package activities

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"orchestrator/pb"
)

type allocatorAccountClient struct {
	allocationsByStatus map[string][]*pb.GPTEmailAllocation
	claimResults        map[string]bool
	claims              []*pb.ClaimGPTEmailAllocationRequest
	aliasRequests       []*pb.CreateGPTEmailAliasAllocationRequest
	aliasCreated        bool
}

func (c *allocatorAccountClient) CreateAccount(context.Context, *pb.CreateAccountRequest, ...grpc.CallOption) (*pb.CreateAccountResponse, error) {
	return nil, nil
}

func (c *allocatorAccountClient) GetAccount(context.Context, *pb.GetAccountRequest, ...grpc.CallOption) (*pb.GetAccountResponse, error) {
	return nil, nil
}

func (c *allocatorAccountClient) UpdateAccount(context.Context, *pb.UpdateAccountRequest, ...grpc.CallOption) (*pb.UpdateAccountResponse, error) {
	return nil, nil
}

func (c *allocatorAccountClient) DeleteAccount(context.Context, *pb.DeleteAccountRequest, ...grpc.CallOption) (*pb.DeleteAccountResponse, error) {
	return nil, nil
}

func (c *allocatorAccountClient) ListAccounts(context.Context, *pb.ListAccountsRequest, ...grpc.CallOption) (*pb.ListAccountsResponse, error) {
	return nil, nil
}

func (c *allocatorAccountClient) UpsertGPTEmailAllocation(context.Context, *pb.UpsertGPTEmailAllocationRequest, ...grpc.CallOption) (*pb.UpsertGPTEmailAllocationResponse, error) {
	return nil, nil
}

func (c *allocatorAccountClient) ListGPTEmailAllocations(_ context.Context, req *pb.ListGPTEmailAllocationsRequest, _ ...grpc.CallOption) (*pb.ListGPTEmailAllocationsResponse, error) {
	allocations := append([]*pb.GPTEmailAllocation{}, c.allocationsByStatus[req.GetStatus()]...)
	if req.GetSplittableOnly() {
		filtered := []*pb.GPTEmailAllocation{}
		for _, allocation := range allocations {
			if allocation.GetIsPrimary() && allocation.GetSplittable() {
				filtered = append(filtered, allocation)
			}
		}
		allocations = filtered
	}
	return &pb.ListGPTEmailAllocationsResponse{Allocations: allocations}, nil
}

func (c *allocatorAccountClient) ClaimGPTEmailAllocation(_ context.Context, req *pb.ClaimGPTEmailAllocationRequest, _ ...grpc.CallOption) (*pb.ClaimGPTEmailAllocationResponse, error) {
	c.claims = append(c.claims, req)
	claimed := c.claimResults[req.GetEmail()]
	return &pb.ClaimGPTEmailAllocationResponse{
		Claimed: claimed,
		Allocation: &pb.GPTEmailAllocation{
			Email:             req.GetEmail(),
			Status:            req.GetStatus(),
			AssignedAccountId: req.GetAccountId(),
		},
	}, nil
}

func (c *allocatorAccountClient) CreateGPTEmailAliasAllocation(_ context.Context, req *pb.CreateGPTEmailAliasAllocationRequest, _ ...grpc.CallOption) (*pb.CreateGPTEmailAliasAllocationResponse, error) {
	c.aliasRequests = append(c.aliasRequests, req)
	return &pb.CreateGPTEmailAliasAllocationResponse{
		Created: c.aliasCreated,
		Allocation: &pb.GPTEmailAllocation{
			Email:             "primary+new@outlook.com",
			PrimaryEmail:      req.GetPrimaryEmail(),
			Status:            emailStatusAssigned,
			AssignedAccountId: req.GetAccountId(),
		},
	}, nil
}

func (c *allocatorAccountClient) MarkGPTEmailAllocationStatus(context.Context, *pb.MarkGPTEmailAllocationStatusRequest, ...grpc.CallOption) (*pb.MarkGPTEmailAllocationStatusResponse, error) {
	return nil, nil
}

func TestAccountEmailAllocatorPrefersAvailablePrimary(t *testing.T) {
	client := &allocatorAccountClient{
		allocationsByStatus: map[string][]*pb.GPTEmailAllocation{
			emailStatusAvailable: {
				{
					Email:        "alias@outlook.com",
					Status:       emailStatusAvailable,
					IsPrimary:    false,
					PrimaryEmail: "registered@outlook.com",
					UpdatedAt:    1,
				},
				{
					Email:        "primary@outlook.com",
					Status:       emailStatusAvailable,
					IsPrimary:    true,
					PrimaryEmail: "primary@outlook.com",
					UpdatedAt:    2,
				},
			},
			emailStatusRegistered: {
				{
					Email:        "registered@outlook.com",
					Status:       emailStatusRegistered,
					IsPrimary:    true,
					PrimaryEmail: "registered@outlook.com",
					Splittable:   true,
				},
			},
		},
		claimResults: map[string]bool{"primary@outlook.com": true},
	}

	email, err := (&accountDBEmailAllocator{accountClient: client}).Allocate(context.Background(), "account-1", nil)
	if err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}
	if email != "primary@outlook.com" {
		t.Fatalf("email = %q, want primary@outlook.com", email)
	}
	if len(client.claims) != 1 || client.claims[0].GetEmail() != "primary@outlook.com" {
		t.Fatalf("claims = %#v, want only primary claim", client.claims)
	}
	if len(client.aliasRequests) != 0 {
		t.Fatalf("alias requests = %d, want 0", len(client.aliasRequests))
	}
}

func TestAccountEmailAllocatorCreatesAliasFromRegisteredPrimary(t *testing.T) {
	client := &allocatorAccountClient{
		allocationsByStatus: map[string][]*pb.GPTEmailAllocation{
			emailStatusAvailable: {},
			emailStatusRegistered: {
				{
					Email:        "primary@outlook.com",
					Status:       emailStatusRegistered,
					IsPrimary:    true,
					PrimaryEmail: "primary@outlook.com",
					Splittable:   true,
				},
			},
		},
		aliasCreated: true,
	}

	email, err := (&accountDBEmailAllocator{accountClient: client}).Allocate(context.Background(), "account-1", nil)
	if err != nil {
		t.Fatalf("Allocate() error = %v", err)
	}
	if email != "primary+new@outlook.com" {
		t.Fatalf("email = %q, want primary+new@outlook.com", email)
	}
	if len(client.aliasRequests) != 1 {
		t.Fatalf("alias requests = %d, want 1", len(client.aliasRequests))
	}
	req := client.aliasRequests[0]
	if req.GetPrimaryEmail() != "primary@outlook.com" || req.GetAccountId() != "account-1" {
		t.Fatalf("alias request = %#v", req)
	}
}
