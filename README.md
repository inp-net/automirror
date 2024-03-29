# automirror

Automatically create a mirror repository on a github organization and add a push mirror to the corresponding gitlab repository

## Usage

### Manual

```bash
git clone https://git.inpt.fr/inp-net/automirror
cd automirror
cp .env.example .env
# Edit .env file to fill with your own values
poetry install
poetry run python main.py
```

### Docker

There's a docker image accessible [at dockerhub](https://hub.docker.com/r/uwun/automirror)

```bash
docker run -v $(pwd)/.env:/app/.env uwun/automirror
```

### Kubernetes

You'll find an example Kubernetes deployment in the `k8s-example` directory that runs the script every monday at 01:00.
