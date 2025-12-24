.PHONY: proto test

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		pkg/types/command.proto

test:
	go test ./... -v

test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out
