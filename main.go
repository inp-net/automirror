package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"

	"dario.cat/mergo"
	"github.com/google/uuid"
	ll "github.com/gwennlbh/label-logger-go"
	"github.com/invopop/jsonschema"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

var Version string
var Commit string

type MirrorDefaults struct {
	Except    []string          `json:"except,omitempty"`
	Only      []string          `json:"only,omitempty"`
	Prefix    string            `json:"prefix,omitempty"`
	Suffix    string            `json:"suffix,omitempty"`
	Topics    []string          `json:"topics,omitempty"`
	Renames   map[string]string `json:"renames,omitempty"`
	Subgroups struct {
		Flatten string `json:"flatten"`
	} `json:"subgroups,omitempty"`
}

type MirrorDefinition struct {
	From      string            `json:"from"`
	Except    []string          `json:"except,omitempty"`
	Only      []string          `json:"only,omitempty"`
	Prefix    string            `json:"prefix,omitempty"`
	Suffix    string            `json:"suffix,omitempty"`
	Topics    []string          `json:"topics,omitempty"`
	Renames   map[string]string `json:"renames,omitempty"`
	Subgroups struct {
		Flatten string `json:"flatten"`
	} `json:"subgroups,omitempty"`
}

// Config holds the configuration
type Config struct {
	To       string                        `json:"to" jsonschema:"enum=github.com"`
	From     string                        `json:"from"`
	Orgs     map[string][]MirrorDefinition `json:"orgs"`
	Defaults MirrorDefaults                `json:"defaults,omitempty"`
}

// Actual configuration
var config Config

func loadConfig(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	decoder := yaml.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return err
	}

	return nil
}

// Env holds the environment variables required for the application.
type Env struct {
	GitHubUsername string
	GitHubToken    string
	GitLabToken    string
}

// Global environment configuration
var env Env

// loadEnv loads environment variables from a .env file or system environment.
func loadEnv() error {
	if _, err := os.Stat(".env"); err == nil {
		err := godotenv.Load(".env")
		if err != nil {
			return err
		}
	}

	env = Env{
		GitHubUsername: os.Getenv("GITHUB_USERNAME"),
		GitHubToken:    os.Getenv("GITHUB_TOKEN"),
		GitLabToken:    os.Getenv("GITLAB_TOKEN"),
	}

	return nil
}

// glabGql sends a GraphQL query to GitLab and returns the response.
func glabGql(query string, variables map[string]interface{}, authed bool) (map[string]interface{}, error) {
	url := fmt.Sprintf("https://%s/api/graphql", config.From)

	jsonBody, err := json.Marshal(map[string]interface{}{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if authed {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", env.GitLabToken))
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var responseBody map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&responseBody); err != nil {
		return nil, err
	}

	if errors, ok := responseBody["errors"]; ok {
		return nil, fmt.Errorf("GraphQL error: %v", errors)
	}

	return responseBody["data"].(map[string]interface{}), nil
}

func githubOrgConfig(org string) ([]MirrorDefinition, bool) {
	if conf, found := config.Orgs[org]; found {
		ll.Debug("Org config for %s: %+v", org, conf)
		merged := make([]MirrorDefinition, len(conf))
		for i, c := range conf {
			merged[i] = c
			mergo.Merge(&merged[i], MirrorDefinition{
				From:      c.From,
				Except:    config.Defaults.Except,
				Only:      config.Defaults.Only,
				Prefix:    config.Defaults.Prefix,
				Suffix:    config.Defaults.Suffix,
				Topics:    config.Defaults.Topics,
				Subgroups: config.Defaults.Subgroups,
				Renames:   config.Defaults.Renames,
			})
		}
		return merged, true
	}

	return []MirrorDefinition{}, false
}

func isGithubRepoEmpty(githubUrl string) (bool, error) {
	cmd := exec.Command("git", "ls-remote", githubUrl)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("while determining if %s is empty: %w", githubUrl, err)
	}

	return len(strings.TrimSpace(string(out))) == 0, nil
}

func initializeGithubRepo(repo map[string]any, githubRepoUrl string) error {
	// Generate uuid for the repo directory
	repoUID := uuid.NewString()

	// Cleanup
	defer func() {
		ll.Debug("Cleaning up %s", repoUID)
		cmd := exec.Command("rm", "-rf", repoUID)
		err := cmd.Run()
		if err != nil {
			ll.Error("Could not delete clone temp. directory %s: %s", repoUID, err)
		}
	}()

	// Clone the repo
	ll.Debug("Cloning %s to %s", repo["httpUrlToRepo"], repoUID)
	cmd := exec.Command("git", "clone", repo["httpUrlToRepo"].(string), repoUID)
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("while cloning %s: %w", repo["httpUrlToRepo"], err)
	}

	// Get default branch name
	cmd = exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoUID
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("while getting default branch name for repo %s at %s: %w", repo["httpUrlToRepo"], repoUID, err)
	}

	defaultBranch := strings.TrimSpace(string(out))
	ll.Debug("Default branch for %s is %s", repo["httpUrlToRepo"], defaultBranch)

	// Configure authentication
	cmd = exec.Command("git", "config", "user.name", env.GitHubUsername)
	cmd.Dir = repoUID
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("while configuring git user.name: %w", err)
	}

	// Push to github
	ll.Debug("Pushing to %s", githubRepoUrl)
	targetUrl, err := url.Parse(githubRepoUrl)
	if err != nil {
		return fmt.Errorf("target github repo url %q is invalid: %w", githubRepoUrl, err)
	}

	// Set credentials
	targetUrl.User = url.UserPassword(env.GitHubUsername, env.GitHubToken)
	cmd = exec.Command("git", "push", targetUrl.String(), defaultBranch)
	cmd.Dir = repoUID
	_, err = cmd.Output()
	if err != nil {
		out = []byte{}
		switch err := err.(type) {
		case *exec.ExitError:
			out = err.Stderr
		}
		return fmt.Errorf("while pushing to %s: %w: %s", githubRepoUrl, err, string(out))
	}

	return nil
}

// upsertGitHubRepo creates or updates a GitHub repository and returns its name.
func upsertGitHubRepo(repo map[string]interface{}, mirrorConfigUsed MirrorDefinition, githubOrg string) (string, error) {

	path := githubNameFromGitlabPath(repo["fullPath"].(string), mirrorConfigUsed)

	description := fmt.Sprintf("%.250s (mirror)", repo["description"])
	description = strings.ReplaceAll(description, "\n", "  ")

	checkUrl := fmt.Sprintf("https://github.com/%s/%s", githubOrg, path)
	resp, err := http.Get(checkUrl)
	if err != nil {
		return "", err
	}

	client := &http.Client{}
	var req *http.Request

	if resp.StatusCode == 200 {
		ll.Log("Updating", "cyan", "%s/%s", githubOrg, path)
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s", githubOrg, path)

		reqBody := map[string]interface{}{
			"description": description,
			"homepage":    repo["webUrl"],
		}
		jsonBody, _ := json.Marshal(reqBody)

		req, err = http.NewRequest("PATCH", url, bytes.NewBuffer(jsonBody))
		if err != nil {
			return "", err
		}
	} else {
		ll.Log("Creating", "magenta", "%s/%s", githubOrg, path)
		url := fmt.Sprintf("https://api.github.com/orgs/%s/repos", githubOrg)

		reqBody := map[string]interface{}{
			"name":        path,
			"description": description,
			"homepage":    repo["webUrl"],
		}
		jsonBody, _ := json.Marshal(reqBody)

		req, err = http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
		if err != nil {
			return "", err
		}
	}

	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", env.GitHubToken))
	req.Header.Set("Content-Type", "application/json")

	ll.Debug("%s %s", req.Method, req.URL)

	resp, err = client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		reqBody, _ := io.ReadAll(req.Body)
		return "", fmt.Errorf("GitHub API error: %s: %s: for request: %s", resp.Status, respBody, reqBody)
	}

	return path, nil
}

// redactUrl removes sensitive information (password) from a URL.
func redactUrl(rawUrl string) string {
	parsedUrl, _ := url.Parse(rawUrl)
	if parsedUrl.User != nil {
		parsedUrl.User = url.UserPassword("---", "---")
	}
	return parsedUrl.String()
}

// setGitLabMirror sets up a GitLab repository to mirror a GitHub repository.
func setGitLabMirror(repo map[string]interface{}, githubName string, githubOrg string) error {
	ll.Debug("Setting up mirror for %+v", repo)
	projectIdUrl, err := url.Parse(repo["id"].(string))
	if err != nil {
		return fmt.Errorf("invalid gitlab project ID %q: %w", repo["id"], err)
	}

	projectID := projectIdUrl.Path
	for strings.Contains(projectID, "/") {
		projectID = strings.SplitN(projectID, "/", 2)[1]
	}
	githubRepoUrl := fmt.Sprintf("https://%s:%s@%s/%s/%s.git", env.GitHubUsername, env.GitHubToken, config.To, githubOrg, githubName)

	mirrorsUrl := fmt.Sprintf("https://%s/api/v4/projects/%s/remote_mirrors", config.From, projectID)
	req, _ := http.NewRequest("GET", mirrorsUrl, nil)
	req.Header.Set("PRIVATE-TOKEN", env.GitLabToken)

	ll.Debug("%s %s", req.Method, req.URL)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var mirrors []map[string]interface{}
	response := new(bytes.Buffer)
	response.ReadFrom(resp.Body)

	if err := json.Unmarshal(response.Bytes(), &mirrors); err != nil {
		return fmt.Errorf("could not decode GitLab API response %q: %w", response.String(), err)
	}

	// Delete existing mirrors for github.com
	for _, mirror := range mirrors {
		mirrorUrl, err := url.Parse(mirror["url"].(string))
		if err != nil {
			continue
		}
		if mirrorUrl.Host == config.To {
			mirrorID := mirror["id"].(float64)
			deleteUrl := fmt.Sprintf("https://%s/api/v4/projects/%s/remote_mirrors/%d", config.From, projectID, int(mirrorID))
			req, err = http.NewRequest("DELETE", deleteUrl, nil)
			if err != nil {
				return fmt.Errorf("could not prepare mirror deletion http request for %s: %w", redactUrl(mirrorUrl.String()), err)
			}

			req.Header.Set("PRIVATE-TOKEN", env.GitLabToken)

			ll.Debug("%s %s", req.Method, req.URL)

			resp, err = client.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 400 {
				return fmt.Errorf("could not delete existing %s mirror %q: GitLab API error: %s", config.To, redactUrl(mirrorUrl.String()), resp.Status)
			}
		}
	}

	ll.Log("Mirroring", "magenta", "%s/%s <- %s", githubOrg, githubName, repo["webUrl"])

	reqBody := map[string]interface{}{
		"enabled":     true,
		"url":         githubRepoUrl,
		"auth_method": "password",
	}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ = http.NewRequest("POST", mirrorsUrl, bytes.NewBuffer(jsonBody))
	req.Header.Set("PRIVATE-TOKEN", env.GitLabToken)
	req.Header.Set("Content-Type", "application/json")

	ll.Debug("%s %s", req.Method, req.URL)

	resp, err = client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

func githubNameFromGitlabPath(path string, mirrorConfigUsed MirrorDefinition) string {
	pathParts := strings.Split(path, "/")[1:]
	path = strings.Join(pathParts, mirrorConfigUsed.Subgroups.Flatten)
	for from, to := range mirrorConfigUsed.Renames {
		if from == path {
			path = to
			break
		}
	}
	path = fmt.Sprintf("%s%s%s", mirrorConfigUsed.Prefix, path, mirrorConfigUsed.Suffix)
	return path
}

// processProject processes a single GitLab project, creating/updating the GitHub repo and setting up the mirror.
func processProject(project map[string]interface{}, mirrorDef MirrorDefinition, org string, wg *sync.WaitGroup) {
	defer wg.Done()

	ll.Debug("Processing project %s", project["webUrl"])

	githubName, err := upsertGitHubRepo(project, mirrorDef, org)
	if err != nil {
		ll.ErrorDisplay("target repo %s: could not upsertGitHubRepo", err, githubNameFromGitlabPath(project["fullPath"].(string), mirrorDef))
		return
	}

	err = setGitLabMirror(project, githubName, org)
	if err != nil {
		ll.ErrorDisplay("target repo %s: could not setGitLabMirror", err, githubName)
		return
	}

	githubRepoUrl := fmt.Sprintf("https://%s/%s/%s", config.To, org, githubName)

	if empty, err := isGithubRepoEmpty(githubRepoUrl); err != nil {
		ll.ErrorDisplay("while checking if %s is empty", err, githubRepoUrl)
	} else if empty {
		ll.Log("Pushing", "green", "%s/%s", org, githubName)
		err = initializeGithubRepo(project, githubRepoUrl)
		if err != nil {
			ll.ErrorDisplay("target repo %s: could not push to github repo", err, githubName)
			return
		}
	} else {
		ll.Debug("Repo %s is not empty, skipping initialization", githubRepoUrl)
	}

	ll.Log("Finished", "green", "%s/%s", org, githubName)
}

func prepareSyncForMirror(githubOrg string, configIndex int, mirror MirrorDefinition) (output []struct {
	mirrorDef   MirrorDefinition
	projects    []map[string]any
	configIndex int
}, err error) {
	query := `
	query($group: ID!) {
	group(fullPath: $group) {
		projects {
			nodes {
				fullPath, webUrl, name, topics, id, description, httpUrlToRepo
			}
		}
}
	}`

	ll.Log("Fetching", "cyan", "projects on https://%s/%s [dim](from config orgs.%s[%d])[reset]", config.From, mirror.From, githubOrg, configIndex)
	ll.Debug("Mirror config: %+v", mirror)
	response, err := glabGql(query, map[string]any{
		"group": mirror.From,
	}, false)

	if err != nil {
		return output, fmt.Errorf("while getting gitlab repos: %w", err)
	}

	if response["group"] == nil {
		return output, fmt.Errorf("group %s not found", mirror.From)
	}

	if resp, ok := response["group"].(map[string]any)["projects"].(map[string]any)["nodes"].([]any); ok {
		projects := make([]map[string]any, 0, len(resp))
	nextproject:
		for _, project := range resp {
			fullpath := project.(map[string]any)["fullPath"].(string)
			ll.Debug("Found project %s", fullpath)
			topics := project.(map[string]any)["topics"].([]any)
			for _, topic := range topics {
				ll.Debug("Project %s has topic %q", fullpath, topic)
				for _, excluded := range mirror.Except {
					if fullpath == excluded {
						ll.Debug("Project %s is excluded", fullpath)
						continue nextproject
					}
				}

				for _, selectedTopics := range mirror.Topics {
					if topic == selectedTopics {
						ll.Debug("Project %s has selected topic %q", fullpath, topic)
						if len(mirror.Only) > 0 {
							ll.Debug("Mirror config defines only some projects")
							for _, only := range mirror.Only {
								if fullpath == only {
									ll.Debug("Project %s is in only list, adding", fullpath)
									projects = append(projects, project.(map[string]any))
									continue nextproject
								}
							}

							ll.Debug("Project %s is not in only list", fullpath)
							continue nextproject
						} else {
							ll.Debug("Adding project %s", fullpath)
							projects = append(projects, project.(map[string]any))
						}
					}
				}
			}
		}

		output = append(output, struct {
			mirrorDef   MirrorDefinition
			projects    []map[string]any
			configIndex int
		}{
			mirrorDef:   mirror,
			projects:    projects,
			configIndex: configIndex,
		})
	} else {
		return output, fmt.Errorf("could not get projects")
	}

	return output, nil
}

func prepareSyncForGithubOrg(org string) (output []struct {
	mirrorDef   MirrorDefinition
	projects    []map[string]any
	configIndex int
}, err error) {
	mirrors, found := githubOrgConfig(org)
	if !found {
		return nil, fmt.Errorf("org %s is not configured", org)
	}

	// Prepare every mirror in parallel, collecting results in output
	var wg sync.WaitGroup
	var preparedChan = make(chan []struct {
		mirrorDef   MirrorDefinition
		projects    []map[string]any
		configIndex int
	}, len(mirrors))

	for i, mirror := range mirrors {
		wg.Add(1)
		go func(i int, mirror MirrorDefinition) {
			defer wg.Done()
			prepared, err := prepareSyncForMirror(org, i, mirror)
			if err != nil {
				ll.ErrorDisplay("while fetching projects for github org %s: could not prepare sync for mirror config orgs.%s[%d]", err, org, org, i)
				return
			}
			preparedChan <- prepared
		}(i, mirror)
	}

	go func() {
		wg.Wait()
		close(preparedChan)
	}()

	for prepared := range preparedChan {
		output = append(output, prepared...)
	}

	return output, nil
}

// main is the entry point of the application.
func main() {
	// --print-jsonschema
	if len(os.Args) > 1 && os.Args[1] == "--print-jsonschema" {
		schema := jsonschema.Reflect(&Config{})
		out, _ := json.Marshal(&schema)
		fmt.Println(string(out))
		return
	}

	ll.Info("Starting automirror v%s (%s)", Version, Commit)

	err := loadEnv()
	if err != nil {
		fmt.Println("Error loading environment variables:", err)
		return
	}

	configPath := "config.yaml"
	if len(os.Args) > 2 && os.Args[1] == "--config" {
		configPath = os.Args[2]
	}

	err = loadConfig(configPath)
	if err != nil {
		fmt.Printf("could not load config: %s\n", err)
		return
	}

	nodes := make([]struct {
		projects  []map[string]any
		org       string
		mirrorDef MirrorDefinition
		i         int
	}, 0)

	for org := range config.Orgs {
		projectsOfOrgByMirror, err := prepareSyncForGithubOrg(org)
		if err != nil {
			ll.ErrorDisplay("could not prep sync for github org %s", err, org)
			return
		}

		for _, projectsOfOrg := range projectsOfOrgByMirror {
			nodes = append(nodes, struct {
				projects  []map[string]any
				org       string
				mirrorDef MirrorDefinition
				i         int
			}{
				i:         projectsOfOrg.configIndex,
				projects:  projectsOfOrg.projects,
				org:       org,
				mirrorDef: projectsOfOrg.mirrorDef,
			})
		}
	}

	var wg sync.WaitGroup

	// mirroringPlan maps github org names to map of github repo names to [gitlab project path, index of config entry in config.Orgs]
	mirroringPlan := make(map[string]map[string][]string)

	for _, node := range nodes {
		for _, project := range node.projects {
			if _, ok := mirroringPlan[node.org]; !ok {
				mirroringPlan[node.org] = make(map[string][]string)
			}
			githubName := githubNameFromGitlabPath(project["fullPath"].(string), node.mirrorDef)
			mirroringPlan[node.org][githubName] = []string{project["fullPath"].(string), fmt.Sprintf("%d", node.i)}
		}
	}

	fmt.Println()
	for org, projects := range mirroringPlan {
		ll.Log("Will sync", "green", "to github.com/[bold]%s[reset]:", org)
		paths := make([]string, 0, len(projects))

		sortedKeys := maps.Keys(projects)
		slices.Sort(sortedKeys)

		for _, project := range sortedKeys {
			paths = append(paths, fmt.Sprintf("%-25s [dim]<--via .%s--[reset] %s", project, projects[project][1], projects[project][0]))
		}

		ll.Log("", "reset",
			ll.List(paths, "[bold][dim]·[reset] %s", "\n"),
		)
	}
	fmt.Println()

	// --plan
	if len(os.Args) > 1 && os.Args[1] == "--plan" {
		return
	}

	for _, node := range nodes {
		for _, project := range node.projects {
			wg.Add(1)
			go func() { processProject(project, node.mirrorDef, node.org, &wg) }()
		}
	}

	wg.Wait()
}
