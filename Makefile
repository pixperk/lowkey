.PHONY: proto test

proto:
	# Generate internal command types
	protoc --go_out=. --go_opt=paths=source_relative \
		pkg/types/command.proto

	# Generate gRPC API
	mkdir -p api/v1
	protoc --go_out=. --go_opt=module=github.com/pixperk/lowkey \
	       --go-grpc_out=. --go-grpc_opt=module=github.com/pixperk/lowkey \
	       api/proto/lock.proto

test:
	go test ./... -v

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out
