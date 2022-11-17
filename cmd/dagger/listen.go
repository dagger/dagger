package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"dagger.io/dagger"
	"github.com/spf13/cobra"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

var listenAddress string

var listenCmd = &cobra.Command{
	Use:     "listen",
	Aliases: []string{"l"},
	Run:     Listen,
	Short:   "Starts the engine server",
}

func Listen(cmd *cobra.Command, args []string) {
	if err := setupServer(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "==> server listening on %s\n", listenAddress)
	err := http.ListenAndServe(listenAddress, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func setupServer(ctx context.Context) error {
	opts := []dagger.ClientOpt{
		dagger.WithWorkdir(workdir),
		dagger.WithConfigPath(configPath),
	}

	if debugLogs {
		opts = append(opts, dagger.WithLogOutput(os.Stdout))
	}

	c, err := dagger.Connect(ctx, opts...)
	if err != nil {
		return err
	}

	http.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		res := make(map[string]interface{})
		resp := &dagger.Response{Data: &res}

		req := map[string]interface{}{
			"query":         "",
			"operationName": "",
		}

		err = json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			fmt.Println(err)
			rw.WriteHeader(http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		err = c.Do(ctx,
			&dagger.Request{
				Query:     req["query"].(string),
				Variables: req["variables"],
				OpName:    req["operationName"].(string),
			},
			resp,
		)

		var gqle gqlerror.List
		if errors.As(err, &gqle) {
			resp.Errors = gqle
		} else if err != nil {
			rw.WriteHeader(http.StatusBadGateway)
			return
		}

		mres, err := json.Marshal(resp)
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			return
		}

		rw.Header().Add("content-type", "application/json")
		rw.Write(mres)
	})
	return nil
}

func init() {
	listenCmd.Flags().StringVarP(&listenAddress, "listen", "", ":8080", "Listen on network address ADDR")
}
