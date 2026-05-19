package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	pb "github.com/pixperk/lowkey/api/v1"
	"github.com/pixperk/lowkey/pkg/gateway"
	"github.com/pixperk/lowkey/pkg/raft"
	"github.com/pixperk/lowkey/pkg/server"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func main() {
	var (
		nodeID    = flag.String("node-id", "", "Unique node ID (generates UUID if empty)")
		raftAddr  = flag.String("raft-addr", "127.0.0.1:7000", "Raft bind address")
		grpcAddr  = flag.String("grpc-addr", ":9000", "gRPC server address")
		httpAddr  = flag.String("http-addr", ":8080", "HTTP gateway address")
		dataDir   = flag.String("data-dir", "./data", "Data directory for Raft storage")
		bootstrap = flag.Bool("bootstrap", false, "Bootstrap a new cluster")
		joinAddr  = flag.String("join", "", "Comma-separated gRPC seed addresses of existing cluster members (e.g. 127.0.0.1:9000,127.0.0.1:9001)")
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
		seeds := splitSeeds(*joinAddr)
		usedSeed, err := joinCluster(seeds, nid, *raftAddr)
		if err != nil {
			log.Fatalf(":( failed to join cluster via %v: %v", seeds, err)
		}
		log.Printf("OwO joined cluster via %s", usedSeed)
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

// splitSeeds parses a comma-separated list of gRPC addresses, trimming whitespace
// and dropping empties.
func splitSeeds(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// joinCluster tries each seed gRPC address in order, asking it to add this node
// as a voter. If a seed responds with NotLeader (codes.Unavailable), the next
// seed is tried — so the caller can list several known cluster members and the
// join succeeds as long as one of them is (or knows of) the leader.
// Returns the seed that accepted the join.
func joinCluster(seeds []string, nodeID uuid.UUID, raftAddr string) (string, error) {
	if len(seeds) == 0 {
		return "", fmt.Errorf("no seed addresses provided")
	}

	var lastErr error
	for _, seed := range seeds {
		err := tryAddPeer(seed, nodeID, raftAddr)
		if err == nil {
			return seed, nil
		}
		lastErr = fmt.Errorf("%s: %w", seed, err)
		// If the seed told us it's not the leader, move on to the next seed.
		if status.Code(err) == codes.Unavailable {
			log.Printf("seed %s is not leader, trying next", seed)
			continue
		}
		// Any other error: also try the next seed but keep the last error.
		log.Printf("seed %s rejected join: %v", seed, err)
	}
	return "", lastErr
}

func tryAddPeer(peerGRPCAddr string, nodeID uuid.UUID, raftAddr string) error {
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
