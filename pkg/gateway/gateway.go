package gateway

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	pb "github.com/pixperk/lowkey/api/v1"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Server struct {
	httpServer *http.Server
	grpcAddr   string
}

func NewServer(httpAddr, grpcAddr string) *Server {
	return &Server{
		httpServer: &http.Server{
			Addr: httpAddr,
		},
		grpcAddr: grpcAddr,
	}
}

func (s *Server) Start(ctx context.Context) error {
	// grpc-gateway mux for gRPC → HTTP translation
	grpcMux := runtime.NewServeMux()

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	err := pb.RegisterLockServiceHandlerFromEndpoint(ctx, grpcMux, s.grpcAddr, opts)
	if err != nil {
		return fmt.Errorf("failed to register gateway: %w", err)
	}

	// wrap grpcMux with standard http.ServeMux to add /metrics endpoint
	rootMux := http.NewServeMux()

	// prometheus metrics endpoint
	rootMux.Handle("/metrics", promhttp.Handler())

	// all other requests go to grpc-gateway
	rootMux.Handle("/", grpcMux)

	s.httpServer.Handler = rootMux

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start HTTP gateway: %w", err)
	}

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
