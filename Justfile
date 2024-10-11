dev:
	go run main.go

build:
	go build -o bin/main main.go

docker:
	docker build -t harbor.k8s.inpt.fr/net7/automirror:latest .
	docker push harbor.k8s.inpt.fr/net7/automirror:latest
