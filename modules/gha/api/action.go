package api

// // Action represents the top-level structure of a GitHub Actions action YAML file.
// type Action struct {
// 	Name        string            `json:"name"`                  // The name of the action.
// 	Description string            `json:"description,omitempty"` // A brief description of what the action does.
// 	Author      string            `json:"author,omitempty"`      // The author of the action.
// 	Inputs      map[string]Input  `json:"inputs,omitempty"`      // Inputs the action accepts.
// 	Outputs     map[string]Output `json:"outputs,omitempty"`     // Outputs the action produces.
// 	Runs        Runs              `json:"runs"`                  // The 'runs' block defines how the action is executed.
// 	Branding    Branding          `json:"branding,omitempty"`    // Optional branding information for the action.
// }
//
// // Input represents a single input parameter that the action accepts.
// type Input struct {
// 	Description string `json:"description,omitempty"` // A brief description of the input parameter.
// 	Required    bool   `json:"required,omitempty"`    // Indicates whether this input is required.
// 	Default     string `json:"default,omitempty"`     // The default value for the input if not provided.
// }
//
// // Output represents a single output that the action produces.
// type Output struct {
// 	Description string `json:"description,omitempty"` // A brief description of the output.
// 	Value       string `json:"value,omitempty"`       // The value expression for the output.
// }
//
// // Runs represents the execution strategy for the action.
// // It can be either a JavaScript action, Docker container action, or Composite action.
// type Runs struct {
// 	Using      string                `json:"using"`                // Specifies the type of action (node16, docker, composite).
// 	Main       string                `json:"main,omitempty"`       // The entry point script for JavaScript or Composite actions.
// 	Pre        string                `json:"pre,omitempty"`        // A script to run before the main action in Docker actions.
// 	Post       string                `json:"post,omitempty"`       // A script to run after the main action in Docker actions.
// 	Image      string                `json:"image,omitempty"`      // The Docker image to use for Docker actions.
// 	Env        map[string]string     `json:"env,omitempty"`        // Environment variables to set during the action.
// 	Args       []string              `json:"args,omitempty"`       // Arguments to pass to the Docker container.
// 	Steps      []CompositeActionStep `json:"steps,omitempty"`      // Steps to run in a composite action.
// 	Shell      string                `json:"shell,omitempty"`      // The shell to use for running commands.
// 	Entrypoint []string              `json:"entrypoint,omitempty"` // The entrypoint for Docker actions.
// }
//
// // Step represents an individual step in a composite action.
// type CompositeActionStep struct {
// 	Name             string            `json:"name,omitempty"`              // The name of the step.
// 	ID               string            `json:"id,omitempty"`                // An ID to reference the step in outputs.
// 	Uses             string            `json:"uses,omitempty"`              // An action to run as part of the step (e.g., actions/checkout@v2).
// 	Run              string            `json:"run,omitempty"`               // A shell command to run as part of the step.
// 	Shell            string            `json:"shell,omitempty"`             // The shell to use for the 'run' command.
// 	With             map[string]string `json:"with,omitempty"`              // Parameters to pass to the action being used.
// 	Env              map[string]string `json:"env,omitempty"`               // Environment variables for the step.
// 	WorkingDirectory string            `json:"working-directory,omitempty"` // The working directory for the step.
// }
//
// // Branding represents optional visual attributes for the action in the GitHub Marketplace.
// type Branding struct {
// 	Icon  string `json:"icon,omitempty"`  // The icon to display for the action.
// 	Color string `json:"color,omitempty"` // The color theme for the action.
// }
