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

## Configuration

The configuration is done through environment variables. You can find an example in the `.env.example` file.

| Variable | Description | Example |
|----------|-------------|---------|
| `GITHUB_TOKEN` | The personal access token to use to authenticate to the github API. Must have the `administration:write` scope, and also permissions to push to the repositories that will be created | |
| `GITHUB_USERNAME` | The username of the github user that will be used to create the mirrored repository | |
| `GITHUB_ORGANIZATION` | The name of the github organization where the mirrored repository will be created. Personal accounts are not supported yet. | |
| `GITLAB_TOKEN` | The personal access token to use to authenticate to the gitlab API. Must have the necessary permissions to query and update push mirrors on a project | |
| `GITLAB_HOST` | URL to the Gitlab instance. | `https://git.inpt.fr` |
| `GITLAB_REPOSITORY_SELECTOR` | A topic (can contain spaces) that will be used to search for projects to mirror in the whole gitlab instance. Only public repositories will be mirrored (the query to get the list of repositories to mirror is done without any authentication) | `mirrored to github` |

### About `GITHUB_TOKEN` and `GITHUB_USERNAME`

Those two variables will be used to construct the mirror URL passed to gitlab, as `https://{USERNAME}:{TOKEN}@github.com/{ORGANIZATION}/{REPOSITORY}`.

