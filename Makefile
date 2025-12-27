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

# Benchmark targets
bench-contention:
	go run examples/benchmark/main.go -mode=contention -clients=5 -duration=20s

bench-parallel:
	go run examples/benchmark/main.go -mode=parallel -clients=5 -duration=20s

bench-sequential:
	go run examples/benchmark/main.go -mode=sequential -duration=20s

bench-all: bench-sequential bench-parallel bench-contention
