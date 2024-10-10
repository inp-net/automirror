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

	"dario.cat/mergo"
	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

type MirrorDefinition struct {
	From      string   `yaml:"from"`
	Except    []string `yaml:"except"`
	Only      []string `yaml:"only"`
	Prefix    string   `yaml:"prefix"`
	Suffix    string   `yaml:"suffix"`
	Topics    []string `yaml:"topics"`
	Subgroups struct {
		Flatten string `yaml:"flatten"`
	}
}

// Config holds the configuration for the
type Config struct {
	To       string                        `yaml:"to" jsonschema:"enum=github.com"`
	From     string                        `yaml:"from"`
	Orgs     map[string][]MirrorDefinition `yaml:"orgs"`
	Defaults MirrorDefinition              `yaml:"defaults"`
}

// Actual configuratio
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
		merged := make([]MirrorDefinition, len(conf))
		for i, c := range conf {
			merged[i] = config.Defaults
			mergo.Merge(&merged[i], c)
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
		fmt.Printf("[%25s] Updating repository %s at %s/%s...\n", path, repo["name"], githubOrg, path)
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
		fmt.Printf("[%25s] Creating repository %s at %s/%s...\n", path, repo["name"], githubOrg, path)
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

	fmt.Printf("[%25s] %s %s\n", path, req.Method, req.URL)

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
	_, found := githubOrgConfig(githubOrg)
	if !found {
		return fmt.Errorf("org %s is not configured", githubOrg)
	}

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
			fmt.Printf("[%25s] Mirror already exists for %s at %s/%s to %s/%s.\n", githubName, repo["name"], config.From, repo["fullPath"], githubOrg, githubName)
			return nil
		}
	}

	fmt.Printf("[%25s] Setting mirror for %s at %s/%s to %s/%s...\n", githubName, repo["name"], config.From, repo["fullPath"], githubOrg, githubName)

	reqBody := map[string]interface{}{
		"enabled":     true,
		"url":         githubRepoUrl,
		"auth_method": "password",
	}
	jsonBody, _ := json.Marshal(reqBody)

	req, _ = http.NewRequest("POST", mirrorsUrl, bytes.NewBuffer(jsonBody))
	req.Header.Set("PRIVATE-TOKEN", env.GitLabToken)
	req.Header.Set("Content-Type", "application/json")

	fmt.Printf("[%25s] %s %s\n", githubName, req.Method, req.URL)

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

	githubName := githubNameFromGitlabPath(project["fullPath"].(string), mirrorDef)
	fmt.Printf("[%25s] Processing project %s\n", githubName, project["fullPath"])

	githubName, err := upsertGitHubRepo(project, mirrorDef, org)
	if err != nil {
		fmt.Printf("[%25s] could not upsertGitHubRepo: %s\n", githubName, err)
		return
	}

	err = setGitLabMirror(project, githubName, org)
	if err != nil {
		fmt.Printf("[%25s] could not setGitLabMirror: %s\n", githubName, err)
		return
	}

	fmt.Printf("[%25s] Done!\n", githubName)
}

func prepareSyncForGithubOrg(org string) ([]struct {
	mirrorDef MirrorDefinition
	projects  []map[string]any
}, error) {
	mirrors, found := githubOrgConfig(org)
	if !found {
		return nil, fmt.Errorf("org %s is not configured", org)
	}

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

	var output []struct {
		mirrorDef MirrorDefinition
		projects  []map[string]any
	}

	for _, mirror := range mirrors {
		fmt.Printf("(%25s) Getting projects on %s for group %s...\n", org, config.From, mirror.From)
		response, err := glabGql(query, map[string]any{
			"group": mirror.From,
		}, false)

		if err != nil {
			return output, fmt.Errorf("while getting gitlab repos: %w", err)
		}

		if response["group"] == nil {
			fmt.Printf("(!%24s) group %s not found\n", org, mirror.From)
			continue
		}

		if resp, ok := response["group"].(map[string]any)["projects"].(map[string]any)["nodes"].([]any); ok {
			projects := make([]map[string]any, 0, len(resp))
		nextproject:
			for _, project := range resp {
				topics := project.(map[string]any)["topics"].([]any)
				for _, topic := range topics {
					for _, excluded := range mirror.Except {
						if project.(map[string]any)["fullPath"] == excluded {
							continue nextproject
						}
					}

					for _, selectedTopics := range mirror.Topics {
						if topic == selectedTopics {
							if len(mirror.Only) > 0 {
								for _, only := range mirror.Only {
									if project.(map[string]any)["fullPath"] == only {
										projects = append(projects, project.(map[string]any))
										continue nextproject
									}
								}
							} else {
								projects = append(projects, project.(map[string]any))
							}
						}
					}
				}
			}

			output = append(output, struct {
				mirrorDef MirrorDefinition
				projects  []map[string]any
			}{
				mirrorDef: mirror,
				projects:  projects,
			})
		} else {
			return output, fmt.Errorf("could not get projects")
		}
	}

	return output, nil
}

// main is the entry point of the application.
func main() {
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
	}, 0)

	for org := range config.Orgs {
		projectsOfOrgByMirror, err := prepareSyncForGithubOrg(org)
		if err != nil {
			fmt.Printf("could not prepa sync for github org %s: %s\n", org, err)
			return
		}

		for _, projectsOfOrg := range projectsOfOrgByMirror {
			nodes = append(nodes, struct {
				projects  []map[string]any
				org       string
				mirrorDef MirrorDefinition
			}{
				projects:  projectsOfOrg.projects,
				org:       org,
				mirrorDef: projectsOfOrg.mirrorDef,
			})
		}
	}

	var wg sync.WaitGroup

	for _, node := range nodes {
		for _, project := range node.projects {
			wg.Add(1)
			go func() { processProject(project, node.mirrorDef, node.org, &wg) }()
		}
	}

	wg.Wait()
}
