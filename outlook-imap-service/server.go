package main

import (
	"context"
	"time"

	pb "outlook-imap-service/pb"
)

type emailServer struct {
	pb.UnimplementedEmailServiceServer
	watcher *MailWatcher
}

func NewEmailServer(watcher *MailWatcher) *emailServer {
	return &emailServer{
		watcher: watcher,
	}
}

func (s *emailServer) WaitForEmail(ctx context.Context, req *pb.WaitForEmailRequest) (*pb.WaitForEmailResponse, error) {
	issuedAfter := unixTime(req.GetIssuedAfterUnix())
	if content, ok := s.watcher.ConsumeCachedOTP(req.EmailAddress, req.SubjectKeyword, issuedAfter); ok {
		return &pb.WaitForEmailResponse{Found: true, ContentExtracted: content}, nil
	}

	respChan := make(chan string, 1)

	s.watcher.AddWaiter(req.EmailAddress, req.SubjectKeyword, respChan, issuedAfter)
	defer s.watcher.RemoveWaiter(req.EmailAddress)

	if content, ok := s.watcher.ConsumeCachedOTP(req.EmailAddress, req.SubjectKeyword, issuedAfter); ok {
		return &pb.WaitForEmailResponse{Found: true, ContentExtracted: content}, nil
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	select {
	case content := <-respChan:
		return &pb.WaitForEmailResponse{Found: true, ContentExtracted: content}, nil
	case <-time.After(timeout):
		return &pb.WaitForEmailResponse{Found: false}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func unixTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0)
}
