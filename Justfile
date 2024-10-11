latest_version := `git describe --tags --abbrev=0 | cut -c 2-`

dev:
	go run main.go

build:
	go build -o bin/main main.go

docker:
	docker build -t harbor.k8s.inpt.fr/net7/automirror:latest .
	docker tag harbor.k8s.inpt.fr/net7/automirror:latest harbor.k8s.inpt.fr/net7/automirror:{{latest_version}} uwun/automirror:{{latest_version}} uwun/automirror:latest
	docker push harbor.k8s.inpt.fr/net7/automirror:latest
	docker push harbor.k8s.inpt.fr/net7/automirror:{{latest_version}}
	docker push uwun/automirror:{{latest_version}}
	docker push uwun/automirror:latest
