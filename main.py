from pydantic import BaseModel
import asyncio
import typed_dotenv
import requests as real_requests
import requests
from urllib.parse import urlparse


def background(f):
    def wrapped(*args, **kwargs):
        return asyncio.get_event_loop().run_in_executor(None, f, *args, **kwargs)

    return wrapped


class Env(BaseModel):
    GITHUB_ORGANIZATION: str
    GITHUB_USERNAME: str
    GITHUB_TOKEN: str
    GITLAB_HOST: str
    GITLAB_TOKEN: str
    GITLAB_REPOSITORY_SELECTOR: str


env = typed_dotenv.load_into(Env, filename=".env")


def glab_gql(query: str, variables: dict, authed=True):
    url = f"{env.GITLAB_HOST}/api/graphql"
    response = real_requests.post(
        url,
        json={"query": query, "variables": variables},
        headers=({"Authorization": f"Bearer {env.GITLAB_TOKEN}"} if authed else {}),
    ).json()
    if "errors" in response:
        raise Exception(response["errors"])
    return response["data"]


def upsert_github_repo(repo) -> bool:
    """
    Returns the github name of the created/updated repository.
    """
    path = repo["fullPath"].split("/")[-1]
    exists = (
        requests.get(f"https://github.com/{env.GITHUB_ORGANIZATION}/{path}").status_code
        == 200
    )
    description = f"{repo['description']:.200} · Mirror of {repo['webUrl']}."
    properties = {"Upstream": repo["webUrl"]}
    if exists:
        print(
            f"[{path:20}] Updating repository {repo['name']} at {env.GITHUB_ORGANIZATION}/{path}..."
        )
        url = f"https://api.github.com/repos/{env.GITHUB_ORGANIZATION}/{path}"
        response = requests.patch(
            url,
            json={"description": description, "custom_properties": properties},
            headers={"Authorization": f"Bearer {env.GITHUB_TOKEN}"},
        )
    else:
        print(
            f"[{path:20}] Creating repository {repo['name']} at {env.GITHUB_ORGANIZATION}/{path}..."
        )
        url = f"https://api.github.com/orgs/{env.GITHUB_ORGANIZATION}/repos"
        response = requests.post(
            url,
            json={
                "name": path,
                "description": description,
                "custom_properties": properties,
            },
            headers={"Authorization": f"Bearer {env.GITHUB_TOKEN}"},
        )
        if not response.ok:
            raise Exception(response.json())

    return path


def redact_url(url: str) -> str:
    url_components = urlparse(url)
    if url_components.username or url_components.password:
        url_components = url_components._replace(
            netloc=f"???:???@{url_components.hostname}",
        )

    return url_components.geturl()


def set_gitlab_mirror(repo, github_name: str) -> None:
    """
    Sets the gitlab mirror of the repository.
    """
    # Mutation does not exist in the GraphQL API, so we have to use the REST API

    # get the project id from the graphql gid
    project_id = repo["id"].split("/")[-1]

    # Get list of remotes to see if the mirror already exists
    response = requests.get(
        f"{env.GITLAB_HOST}/api/v4/projects/{project_id}/remote_mirrors",
        headers={"PRIVATE-TOKEN": env.GITLAB_TOKEN},
    ).json()

    url = f"https://{env.GITHUB_USERNAME}:{env.GITHUB_TOKEN}@github.com/{env.GITHUB_ORGANIZATION}/{github_name}.git"

    exists = any(redact_url(mirror["url"]) == redact_url(url) for mirror in response)

    if exists:
        print(
            f"[{github_name:20}] Mirror already exists for {repo['name']} at {env.GITLAB_HOST}/{repo['fullPath']} to {env.GITHUB_ORGANIZATION}/{github_name}."
        )
        return

    print(
        f"[{github_name:20}] Setting mirror for {repo['name']} at {env.GITLAB_HOST}/{repo['fullPath']} to {env.GITHUB_ORGANIZATION}/{github_name}..."
    )
    response = requests.post(
        f"{env.GITLAB_HOST}/api/v4/projects/{project_id}/remote_mirrors",
        json={
            "enabled": True,
            "url": url,
            "auth_method": "password",
        },
        headers={"PRIVATE-TOKEN": env.GITLAB_TOKEN},
    ).json()

    return response


@background
def process_project(project):
    github_name = project["fullPath"].split("/")[-1]
    print(f"[{github_name:20}] Processing project", project["fullPath"])
    github_name = upsert_github_repo(project)
    set_gitlab_mirror(project, github_name)
    print(f"[{github_name:20}] Done!")


print(
    f"Getting public repositories with tag {env.GITLAB_REPOSITORY_SELECTOR} at {env.GITLAB_HOST}...",
    end="\n\n",
)
# Do un unauthed query to only get public repositories
response = glab_gql(
    """
    query($selector: [String!]!) {
        projects(topics: $selector) {
            nodes {
                fullPath, webUrl, name, description, id
            }
        }
    }
    """,
    {"selector": [env.GITLAB_REPOSITORY_SELECTOR]},
    authed=False,
)

for project in response["projects"]["nodes"]:
    process_project(project)
