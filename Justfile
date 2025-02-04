latest_version := `git describe --tags --abbrev=0 | cut -c 2-`

dev:
	go run main.go

build:
	go build -o bin/main main.go
