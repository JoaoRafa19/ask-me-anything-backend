all:
	echo build

run:
	go run ./cmd/wsrs/main.go

gen:
	go generate ./...
