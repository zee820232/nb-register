package main

import (
	"log"
	"net"

	"google.golang.org/grpc"

	pb "outlook-imap-service/pb"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := LoadConfig()
	watcher := NewMailWatcher(cfg)

	// Start polling in the background
	watcher.Start()

	// Start gRPC Server
	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterEmailServiceServer(grpcServer, NewEmailServer(watcher))

	log.Printf("Starting Outlook mail gRPC server on %s...", cfg.ListenAddr)
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("failed to serve gRPC: %v", err)
	}
}
