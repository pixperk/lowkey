package gateway

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	pb "github.com/pixperk/lowkey/api/v1"
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
	mux := runtime.NewServeMux()

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	err := pb.RegisterLockServiceHandlerFromEndpoint(ctx, mux, s.grpcAddr, opts)
	if err != nil {
		return fmt.Errorf("failed to register gateway: %w", err)
	}

	s.httpServer.Handler = mux

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("failed to start HTTP gateway: %w", err)
	}

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
