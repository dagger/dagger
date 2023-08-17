package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	"golang.org/x/sync/errgroup"
)

func (env *Environment) WithCheck(
	check *Check,
	returnType CheckEntrypointReturnType,
	envCache *EnvironmentCache,
) (*Environment, error) {
	env = env.Clone()
	check = check.Clone()
	if returnType != "" {
		check.ReturnType = returnType
	}
	env.Checks = append(env.Checks, check)
	return env, env.updateEnv()
}

type Check struct {
	EnvironmentID EnvironmentID `json:"environmentId,omitempty"`

	Name        string   `json:"name"`
	Description string   `json:"description"`
	Subchecks   []*Check `json:"subchecks"`

	// The container to exec if the check's success+output is being resolved
	// from a user-defined container via the Check.withContainer API
	UserContainer *Container `json:"user_container"`

	// If this check is evaluated via an environment entrypoint, this is the
	// return type of the entrypoint.
	ReturnType CheckEntrypointReturnType `json:"returnType"`
}

func (check *Check) PBDefinitions() ([]*pb.Definition, error) {
	var defs []*pb.Definition
	if check.UserContainer != nil {
		ctrDefs, err := check.UserContainer.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, ctrDefs...)
	}
	for _, subcheck := range check.Subchecks {
		subcheckDefs, err := subcheck.PBDefinitions()
		if err != nil {
			return nil, err
		}
		defs = append(defs, subcheckDefs...)
	}
	return defs, nil
}

func (check *Check) ID() (CheckID, error) {
	return resourceid.Encode(check)
}

func (check *Check) Digest() (digest.Digest, error) {
	return stableDigest(check)
}

func (check Check) Clone() *Check {
	cp := Check{
		Name:          check.Name,
		Description:   check.Description,
		Subchecks:     make([]*Check, len(check.Subchecks)),
		EnvironmentID: check.EnvironmentID,
		ReturnType:    check.ReturnType,
	}
	for i, subcheck := range check.Subchecks {
		cp.Subchecks[i] = subcheck.Clone()
	}
	if check.UserContainer != nil {
		cp.UserContainer = check.UserContainer.Clone()
	}
	return &cp
}

func (check *Check) WithName(name string) *Check {
	check = check.Clone()
	check.Name = name
	return check
}

func (check *Check) WithDescription(description string) *Check {
	check = check.Clone()
	check.Description = description
	return check
}

func (check *Check) WithSubcheck(subcheck *Check) *Check {
	check = check.Clone()
	check.Subchecks = append(check.Subchecks, subcheck)
	return check
}

func (check *Check) WithUserContainer(ctr *Container) *Check {
	check = check.Clone()
	check.UserContainer = ctr
	return check
}

func (check *Check) cacheExitCode() (uint32, error) {
	switch check.ReturnType {
	case CheckReturnTypeVoid:
		return 1, nil
	case CheckReturnTypeString:
		return 1, nil
	case CheckReturnTypeCheckResult:
		return 0, nil
	case CheckReturnTypeCheck:
		return 0, nil
	default:
		return 0, fmt.Errorf("unhandled check return type %q", check.ReturnType)
	}
}

func (check *Check) GetSubchecks(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	envCache *EnvironmentCache,
	installDeps InstallDepsCallback,
) (_ []*Check, rerr error) {
	if len(check.Subchecks) > 0 {
		return check.Subchecks, nil
	}

	if check.UserContainer != nil {
		// no subchecks on user containers
		return nil, nil
	}

	if check.EnvironmentID != "" {
		switch check.ReturnType {
		case CheckReturnTypeVoid:
			return nil, nil
		case CheckReturnTypeString:
			return nil, nil
		case CheckReturnTypeCheckResult:
			return nil, nil
		case CheckReturnTypeCheck:
			// need to exec the entrypoint to get the check and see if there are any subchecks
			env, err := check.EnvironmentID.Decode()
			if err != nil {
				return nil, fmt.Errorf("failed to decode environment for check %q: %w", check.Name, err)
			}
			cacheExitCode, err := check.cacheExitCode()
			if err != nil {
				return nil, fmt.Errorf("failed to get cache exit code for check %q: %w", check.Name, err)
			}
			resource, _, _, err := env.execEnvironment(ctx, bk, progSock, envCache, installDeps, check.Name, nil, cacheExitCode)
			if err != nil {
				return nil, fmt.Errorf("failed to exec check environment container: %w", err)
			}
			recursiveCheck, ok := resource.(*Check)
			if !ok {
				return nil, fmt.Errorf("unexpected check result type %T", resource)
			}
			return recursiveCheck.GetSubchecks(ctx, bk, progSock, pipeline, envCache, installDeps)
		default:
			return nil, fmt.Errorf("unhandled check return type %q for check %q", check.ReturnType, check.Name)
		}
	}

	return nil, nil
}

func (check *Check) Result(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	pipeline pipeline.Path,
	envCache *EnvironmentCache,
	installDeps InstallDepsCallback,
) (*CheckResult, error) {
	if len(check.Subchecks) > 0 {
		// This is a composite check, evaluate it by evaluating each subcheck
		var eg errgroup.Group
		success := true
		var output string
		for _, subcheck := range check.Subchecks {
			subcheck := subcheck
			eg.Go(func() error {
				subresult, err := subcheck.Result(ctx, bk, progSock, pipeline, envCache, installDeps)
				if err != nil {
					return fmt.Errorf("failed to get subcheck result for %q: %w", subcheck.Name, err)
				}
				if !subresult.Success {
					success = false
					output += fmt.Sprintf("Subcheck %q failed:\n%s\n", subcheck.Name, subresult.Output)
				} else {
					output += fmt.Sprintf("Subcheck %q succeeded:\n%s\n", subcheck.Name, subresult.Output)
				}
				return nil
			})
		}
		if err := eg.Wait(); err != nil {
			return nil, err
		}
		return &CheckResult{
			Success: success,
			Output:  "",
		}, nil
	}

	if check.UserContainer != nil {
		// check will be evaluated by exec'ing this container, with success based on exit code
		ctr := check.UserContainer
		success := true
		var output string
		_, evalErr := ctr.Evaluate(ctx, bk)
		var execErr *buildkit.ExecError
		switch {
		case errors.As(evalErr, &execErr):
			success = false
			// TODO: really need combined stdout/stderr now
			output = strings.Join([]string{evalErr.Error(), execErr.Stdout, execErr.Stderr}, "\n\n")
		case evalErr != nil:
			return nil, fmt.Errorf("failed to exec check user container: %w", evalErr)
		default:
			stdout, err := ctr.MetaFileContents(ctx, bk, progSock, "stdout")
			if err != nil {
				return nil, fmt.Errorf("failed to get stdout from check user container: %w", err)
			}
			stderr, err := ctr.MetaFileContents(ctx, bk, progSock, "stderr")
			if err != nil {
				return nil, fmt.Errorf("failed to get stderr from check user container: %w", err)
			}
			// TODO: really need combined stdout/stderr now
			output = strings.Join([]string{stdout, stderr}, "\n\n")
		}
		return &CheckResult{
			Success: success,
			Output:  output,
		}, nil
	}

	// the check is coming from an environment entrypoint, which we will need to exec
	if check.EnvironmentID == "" {
		return nil, fmt.Errorf("invalid empty check %q", check.Name)
	}

	env, err := check.EnvironmentID.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode environment for check %q: %w", check.Name, err)
	}

	cacheExitCode, err := check.cacheExitCode()
	if err != nil {
		return nil, fmt.Errorf("failed to get cache exit code for check %q: %w", check.Name, err)
	}
	resource, outputBytes, exitCode, err := env.execEnvironment(ctx, bk, progSock, envCache, installDeps, check.Name, nil, cacheExitCode)
	if err != nil {
		return nil, fmt.Errorf("failed to exec check environment container: %w", err)
	}

	switch check.ReturnType {
	case CheckReturnTypeVoid:
		return &CheckResult{
			Success: exitCode == 0,
			Output:  string(outputBytes),
		}, nil
	case CheckReturnTypeString:
		return &CheckResult{
			Success: exitCode == 0,
			Output:  string(outputBytes),
		}, nil
	case CheckReturnTypeCheckResult:
		checkResult, ok := resource.(*CheckResult)
		if !ok {
			return nil, fmt.Errorf("unexpected check result type %T", resource)
		}
		return checkResult, nil
	case CheckReturnTypeCheck:
		recursiveCheck, ok := resource.(*Check)
		if !ok {
			return nil, fmt.Errorf("unexpected check result type %T", resource)
		}
		res, err := recursiveCheck.Result(ctx, bk, progSock, pipeline, envCache, installDeps)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate recursive check: %w", err)
		}
		return res, nil
	default:
		return nil, fmt.Errorf("unhandled check return type %q", check.ReturnType)
	}
}

type CheckResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
}

func (checkResult *CheckResult) ID() (CheckResultID, error) {
	return resourceid.Encode(checkResult)
}

func (checkResult *CheckResult) Digest() (digest.Digest, error) {
	return stableDigest(checkResult)
}

func (checkResult CheckResult) Clone() *CheckResult {
	cp := checkResult
	return &cp
}

type CheckEntrypointReturnType string

const (
	CheckReturnTypeVoid        CheckEntrypointReturnType = "CheckEntrypointReturnVoid"
	CheckReturnTypeCheck       CheckEntrypointReturnType = "CheckEntrypointReturnCheck"
	CheckReturnTypeCheckResult CheckEntrypointReturnType = "CheckEntrypointReturnCheckResult"
	CheckReturnTypeString      CheckEntrypointReturnType = "CheckEntrypointReturnString"
)
