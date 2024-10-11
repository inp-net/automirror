package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"

	"dario.cat/mergo"
	ll "github.com/ewen-lbh/label-logger-go"
	"github.com/invopop/jsonschema"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type MirrorDefaults struct {
	Except    []string `json:"except,omitempty"`
	Only      []string `json:"only,omitempty"`
	Prefix    string   `json:"prefix,omitempty"`
	Suffix    string   `json:"suffix,omitempty"`
	Topics    []string `json:"topics,omitempty"`
	Subgroups struct {
		Flatten string `json:"flatten"`
	} `json:"subgroups,omitempty"`
}

type MirrorDefinition struct {
	From      string   `json:"from"`
	Except    []string `json:"except,omitempty"`
	Only      []string `json:"only,omitempty"`
	Prefix    string   `json:"prefix,omitempty"`
	Suffix    string   `json:"suffix,omitempty"`
	Topics    []string `json:"topics,omitempty"`
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
	if os.Getenv("ENV") == "dev" {
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
			})
		}
		return merged, true
	}

	return []MirrorDefinition{}, false
}

// upsertGitHubRepo creates or updates a GitHub repository and returns its name.
func upsertGitHubRepo(repo map[string]interface{}, mirrorConfigUsed MirrorDefinition, githubOrg string) (string, error) {

	path := githubNameFromGitlabPath(repo["fullPath"].(string), mirrorConfigUsed)

	description := fmt.Sprintf("%.200s · Mirror of %s.", repo["description"], repo["webUrl"])

	checkUrl := fmt.Sprintf("https://github.com/%s/%s", githubOrg, path)
	resp, err := http.Get(checkUrl)
	if err != nil {
		return "", err
	}

	// client := &http.Client{}
	var req *http.Request

	if resp.StatusCode == 200 {
		ll.Log("Updating", "cyan", "%s/%s", githubOrg, path)
		url := fmt.Sprintf("https://api.github.com/repos/%s/%s", githubOrg, path)

		reqBody := map[string]interface{}{
			"description": description,
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

	// resp, err = client.Do(req)
	// if err != nil {
	// 	return "", err
	// }
	// defer resp.Body.Close()

	// if resp.StatusCode >= 400 {
	// 	return "", fmt.Errorf("GitHub API error: %s", resp.Status)
	// }

	return path, nil
}

// redactUrl removes sensitive information (username/password) from a URL.
func redactUrl(rawUrl string) string {
	parsedUrl, _ := url.Parse(rawUrl)
	if parsedUrl.User != nil {
		parsedUrl.User = url.UserPassword("???", "???")
	}
	return parsedUrl.String()
}

// setGitLabMirror sets up a GitLab repository to mirror a GitHub repository.
func setGitLabMirror(repo map[string]interface{}, githubName string, githubOrg string) error {
	projectID := strings.Split(repo["id"].(string), "/")[1]
	githubRepoUrl := fmt.Sprintf("https://%s:%s@%s/%s/%s.git", env.GitHubUsername, env.GitHubToken, config.To, githubOrg, githubName)

	mirrorsUrl := fmt.Sprintf("https://%s/api/v4/projects/%s/remote_mirrors", config.From, projectID)
	req, _ := http.NewRequest("GET", mirrorsUrl, nil)
	req.Header.Set("PRIVATE-TOKEN", env.GitLabToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var mirrors []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&mirrors); err != nil {
		return err
	}

	for _, mirror := range mirrors {
		if redactUrl(mirror["url"].(string)) == redactUrl(githubRepoUrl) {
			ll.Info("Mirror already exists for %s at %s/%s to %s/%s.", repo["name"], config.From, repo["fullPath"], githubOrg, githubName)
			return nil
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

	// resp, err = client.Do(req)
	// if err != nil {
	// 	return err
	// }
	// defer resp.Body.Close()

	return nil
}

func githubNameFromGitlabPath(path string, mirrorConfigUsed MirrorDefinition) string {
	pathParts := strings.Split(path, "/")[1:]
	path = strings.Join(pathParts, mirrorConfigUsed.Subgroups.Flatten)
	path = fmt.Sprintf("%s%s%s", mirrorConfigUsed.Prefix, path, mirrorConfigUsed.Suffix)
	return path
}

// processProject processes a single GitLab project, creating/updating the GitHub repo and setting up the mirror.
func processProject(project map[string]interface{}, mirrorDef MirrorDefinition, org string, wg *sync.WaitGroup) {
	defer wg.Done()

	ll.Debug("Processing project %s", project["webUrl"])

	githubName, err := upsertGitHubRepo(project, mirrorDef, org)
	if err != nil {
		ll.ErrorDisplay("%s: could not upsertGitHubRepo", err, githubName)
		return
	}

	err = setGitLabMirror(project, githubName, org)
	if err != nil {
		ll.ErrorDisplay("%s: could not setGitLabMirror", err, githubName)
		return
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
				fullPath, webUrl, name, topics, id
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

	err := loadEnv()
	if err != nil {
		fmt.Println("Error loading environment variables:", err)
		return
	}

	err = loadConfig("config.yaml")
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
