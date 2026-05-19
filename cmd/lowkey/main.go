package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	pb "github.com/pixperk/lowkey/api/v1"
	"github.com/pixperk/lowkey/pkg/gateway"
	"github.com/pixperk/lowkey/pkg/raft"
	"github.com/pixperk/lowkey/pkg/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	var (
		nodeID    = flag.String("node-id", "", "Unique node ID (generates UUID if empty)")
		raftAddr  = flag.String("raft-addr", "127.0.0.1:7000", "Raft bind address")
		grpcAddr  = flag.String("grpc-addr", ":9000", "gRPC server address")
		httpAddr  = flag.String("http-addr", ":8080", "HTTP gateway address")
		dataDir   = flag.String("data-dir", "./data", "Data directory for Raft storage")
		bootstrap = flag.Bool("bootstrap", false, "Bootstrap a new cluster")
		joinAddr  = flag.String("join", "", "gRPC address of an existing cluster member to join (e.g. 127.0.0.1:9000)")
	)
	flag.Parse()

	if *bootstrap && *joinAddr != "" {
		log.Fatal(":( cannot use --bootstrap and --join together")
	}

	var nid uuid.UUID
	var err error
	if *nodeID == "" {
		nid = uuid.New()
		log.Printf("generated node id : %s", nid)
	} else {
		nid, err = uuid.Parse(*nodeID)
		if err != nil {
			log.Fatalf("invalid node id: %v", err)
		}
	}

	log.Printf("Starting lowkey node...")
	log.Printf("  Node ID: %s", nid)
	log.Printf("  Raft: %s", *raftAddr)
	log.Printf("  gRPC: %s", *grpcAddr)
	log.Printf("  HTTP: %s", *httpAddr)
	log.Printf("  Data: %s", *dataDir)
	log.Printf("  Bootstrap: %v", *bootstrap)

	node, err := raft.NewNode(&raft.Config{
		NodeID:    nid,
		BindAddr:  *raftAddr,
		DataDir:   *dataDir,
		Bootstrap: *bootstrap,
		JoinAddr:  *joinAddr,
	})
	if err != nil {
		log.Fatalf("Failed to create Raft node: %v", err)
	}
	defer node.Shutdown()

	log.Println("OwO Raft node initialized")

	grpcServer := grpc.NewServer()
	pb.RegisterLockServiceServer(grpcServer, server.NewServer(node))

	listener, err := net.Listen("tcp", *grpcAddr)
	if err != nil {
		log.Fatalf(":( failed to listen on %s: %v", *grpcAddr, err)
	}

	go func() {
		log.Printf("OwO gRPC server listening on %s", *grpcAddr)
		if err := grpcServer.Serve(listener); err != nil {
			log.Fatalf(":( gRPC server failed: %v", err)
		}
	}()

	_, grpcPort, err := net.SplitHostPort(*grpcAddr)
	if err != nil {
		log.Fatalf(":( invalid grpc-addr %q: %v", *grpcAddr, err)
	}
	gwServer := gateway.NewServer(*httpAddr, net.JoinHostPort("localhost", grpcPort))
	go func() {
		log.Printf("OwO HTTP gateway listening on %s", *httpAddr)
		if err := gwServer.Start(context.Background()); err != nil {
			log.Fatalf(":( HTTP gateway failed: %v", err)
		}
	}()

	if *joinAddr != "" {
		if err := joinCluster(*joinAddr, nid, *raftAddr); err != nil {
			log.Fatalf(":( failed to join cluster via %s: %v", *joinAddr, err)
		}
		log.Printf("OwO joined cluster via %s", *joinAddr)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Println("OwO lowkey is ready")
	log.Println("  Press Ctrl+C to stop")

	<-sigCh
	log.Println("\nShutting down gracefully...")

	// Graceful shutdown
	grpcServer.GracefulStop()
	gwServer.Stop(context.Background())

	log.Println(":} Shutdown complete")
}

// joinCluster dials an existing cluster member's gRPC endpoint and asks it
// to add this node as a voter.
func joinCluster(peerGRPCAddr string, nodeID uuid.UUID, raftAddr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(peerGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	client := pb.NewLockServiceClient(conn)
	_, err = client.AddPeer(ctx, &pb.AddPeerRequest{
		NodeId:      nodeID.String(),
		RaftAddress: raftAddr,
	})
	return err
}
