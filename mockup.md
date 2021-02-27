
# Concepts

- Environment: a hermetic sandbox for loading configuration, executing pipelines, and storing state.
- Workspace: a logical group of environments
- Pipeline: a script which dagger can execute in an environment

# Commands

## Core commands

dagger info			Show information about the dagger installation
dagger compute			Run an environment's compute pipelines and save the result
dagger query			Query an environment's state
dagger configure		Change an environment's configuration
dagger history			Show an environment's history of changes

## Workspace management commands

dagger workspace create		Create a workspace
dagger workspace destroy	Destroy a workspace
dagger workspace info		Show information about a workspace
dagger workspace join		Join a workspace
dagger workspace leave		Leave a workspace
dagger workspace list		List available workspaces
dagger workspace backup		Save a workspace's contents to a backup archive

## Environment management commands

dagger env create		Create an environment
dagger env destroy		Destroy an environment
dagger env info			Show information about an environment
dagger env update		Update an environment
dagger env list			List available environments

## Cloud commands

dagger login			Login to Dagger Cloud (optional)
dagger logout			Logout from Dagger Cloud (optional)
