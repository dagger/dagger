package main

import (
	"strings"

	"github.com/dagger/dagger/modules/gha/api"
)

type Permission string

type Permissions []Permission

func (perms Permissions) Permissions() *api.Permissions {
	if perms == nil {
		return nil
	}
	p := new(api.Permissions)
	for _, perm := range perms {
		switch perm.Object() {
		case "contents":
			p.Contents = perm.Level()
		case "issues":
			p.Issues = perm.Level()
		case "actions":
			p.Actions = perm.Level()
		case "packages":
			p.Packages = perm.Level()
		case "deployments":
			p.Deployments = perm.Level()
		case "pull_requests":
			p.PullRequests = perm.Level()
		case "pages":
			p.Pages = perm.Level()
		case "id_token":
			p.IDToken = perm.Level()
		case "repository_projects":
			p.RepositoryProjects = perm.Level()
		case "statuses":
			p.Statuses = perm.Level()
		case "metadata":
			p.Metadata = perm.Level()
		case "checks":
			p.Checks = perm.Level()
		case "discussions":
			p.Discussions = perm.Level()
		}
	}
	return p
}

func (p Permission) parts() (api.PermissionLevel, string) {
	parts := strings.SplitN(string(p), "_", 2)
	level := api.PermissionLevel(parts[0])
	var object string
	if len(parts) >= 2 {
		object = parts[1]
	}
	return level, object
}

func (p Permission) Level() api.PermissionLevel {
	level, _ := p.parts()
	return level
}

func (p Permission) Object() string {
	_, object := p.parts()
	return object
}

const (
	ReadContents            Permission = "read_contents"
	ReadIssues              Permission = "read_issues"
	ReadActions             Permission = "read_actions"
	ReadPackages            Permission = "read_packages"
	ReadDeployments         Permission = "read_deployments"
	ReadPullRequests        Permission = "read_pull_requests"
	ReadPages               Permission = "read_pages"
	ReadIDToken             Permission = "read_id_token"
	ReadRepositoryProjects  Permission = "read_repository_projects"
	ReadStatuses            Permission = "read_statuses"
	ReadMetadata            Permission = "read_metadata"
	ReadChecks              Permission = "read_checks"
	ReadDiscussions         Permission = "read_discussions"
	WriteContents           Permission = "write_contents"
	WriteIssues             Permission = "write_issues"
	WriteActions            Permission = "write_actions"
	WritePackages           Permission = "write_packages"
	WriteDeployments        Permission = "write_deployments"
	WritePullRequests       Permission = "write_pull_requests"
	WritePages              Permission = "write_pages"
	WriteIDToken            Permission = "write_id_token"
	WriteRepositoryProjects Permission = "write_repository_projects"
	WriteStatuses           Permission = "write_statuses"
	WriteMetadata           Permission = "write_metadata"
	WriteChecks             Permission = "write_checks"
	WriteDiscussions        Permission = "write_discussions"
)
