.PHONY: proto test bench

proto:
	# Generate internal command types
	protoc --go_out=. --go_opt=paths=source_relative \
		pkg/types/command.proto

	# Generate gRPC API + HTTP Gateway
	mkdir -p api/v1
	protoc -I api/proto \
	       --go_out=. --go_opt=module=github.com/pixperk/lowkey \
	       --go-grpc_out=. --go-grpc_opt=module=github.com/pixperk/lowkey \
	       --grpc-gateway_out=. --grpc-gateway_opt=module=github.com/pixperk/lowkey \
	       --grpc-gateway_opt=generate_unbound_methods=true \
	       api/proto/lock.proto

test:
	go test ./... -v

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

# Benchmark targets (using Go's built-in benchmarking)
bench-sequential:
	go test -bench=Sequential -benchtime=10s ./pkg/client/

bench-parallel:
	go test -bench=Parallel -benchtime=10s ./pkg/client/

bench-contention:
	go test -bench=Contention -benchtime=10s ./pkg/client/

bench-all:
	go test -bench=. -benchtime=10s ./pkg/client/

# Percentile benchmarks (p50, p90, p99, p99.9)
bench-percentiles:
	go test -run=Percentile -v ./pkg/client/

# Observability stack
obs-up:
	docker-compose up -d
	@echo ""
	@echo "✓ Prometheus running at http://localhost:9090"
	@echo "✓ Grafana running at http://localhost:3000 (admin/admin)"
	@echo ""
	@echo "Next: Import grafana-dashboard.json via Grafana UI"

obs-down:
	docker-compose down

obs-logs:
	docker-compose logs -f
