# <center> <img src="./wordmark.png">  </center>

Automatically create a mirror repository on a github organization and add a push mirror to the corresponding gitlab repository

## Usage

### Manual

```bash
git clone https://git.inpt.fr/inp-net/automirror
cd automirror
cp .env.example .env
# Edit .env file to fill with your own values
go run main.go
```

### Docker

There's a docker image accessible [at dockerhub](https://hub.docker.com/r/uwun/automirror)

```bash
docker run -v $(pwd)/.env:/app/.env uwun/automirror
```

### Kubernetes

You'll find an example Kubernetes deployment in the `k8s-example` directory that runs the script every monday at 01:00.

## Configuration

### Credentials

Credentials are set with environment variables. Automirror will also load a .env if the file exists. You can find an example in the `.env.example` file.

| Variable | Description | Example |
|----------|-------------|---------|
| `GITHUB_TOKEN` | The personal access token to use to authenticate to the github API. Must have the `administration:write` scope, and also permissions to push to the repositories that will be created | |
| `GITHUB_USERNAME` | The username of the github user that will be used to create the mirrored repositories | |
| `GITLAB_TOKEN` | The personal access token to use to authenticate to the gitlab API. Must have the necessary permissions to query and update push mirrors on a project | |

`GITHUB_TOKEN` and `GITHUB_USERNAME` will be used to construct the mirror URL passed to gitlab, as `https://{USERNAME}:{TOKEN}@github.com/{ORGANIZATION}/{REPOSITORY}`.

### Mirrors

Configuration of mirrors is defined in a YAML file. A JSON Schema is available at `config.schema.json`, and example config files are in `examples/`.

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/ewen-lbh/automirror/main/config.schema.json
to: github.com # Target git host. Only github.com is supported for now.
from: my-gitlab.example.com # Source git host. Only gitlab hosts are supported for now.

# Set defaults to avoid repeating yourself
defaults:
    topics: [mirrored to github] # selects repositories that have one of these topics. Providing an empty array selects no repositories.
    subgroups: 
        flatten: "-" # with what character to join gitlab sub-groups to compute the github repo name. For example, if the gitlab project is `group/subgroup/project`, the github repo name will be `subgroup-project`
    
orgs:
    # Repositories to mirror to github.com/my-github-org
    my-github-org:
        - from: foo # Mirror all public repositories from gitlab org "foo" that have the topic "mirrored to github" (see `defaults`)
        - from: bar 
          prefix: baz # Prefix the github repository names with "baz"
          # there's also `suffix`
        - from: qux 
          except: [qux/example] # Don't mirror the repository "qux/example"
        - from: spam 
          only: [spam/eggs, spam/bacon] # Only mirror repositories "spam/eggs" and "spam/bacon"
```
        
