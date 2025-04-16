package main

import (
	"context"
	"dagger/botsbuildingbots/internal/dagger"
	"dagger/botsbuildingbots/internal/telemetry"
	"encoding/csv"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

func (result *EvalsAcrossModels) CSV(
	// Include a header.
	// +default=false
	header bool,
) string {
	buf := new(strings.Builder)
	csvW := csv.NewWriter(buf)
	if header {
		csvW.Write([]string{
			"model",
			"eval",
			"input_tokens",
			"output_tokens",
			"total_attempts",
			"success_rate",
		})
	}
	for _, result := range result.ModelResults {
		for _, eval := range result.EvalReports {
			csvW.Write([]string{
				result.ModelName,
				eval.Name,
				fmt.Sprintf("%d", eval.InputTokens),
				fmt.Sprintf("%d", eval.OutputTokens),
				fmt.Sprintf("%d", eval.TotalAttempts),
				fmt.Sprintf("%0.2f", eval.SuccessRate),
			})
		}
	}
	csvW.Flush()
	return buf.String()
}

func (m *Evaluator) Compare(
	ctx context.Context,
	before *dagger.File,
	after *dagger.File,
) error {
	// Parse the before and after CSV files to extract data
	beforeData, err := parseCSVData(ctx, before)
	if err != nil {
		return err
	}
	afterData, err := parseCSVData(ctx, after)
	if err != nil {
		return err
	}

	// Calculate aggregates for before and after
	beforeAggregates := aggregateData(beforeData)
	afterAggregates := aggregateData(afterData)

	// Build comparison report
	ctx, span := Tracer().Start(ctx, "report",
		telemetry.Reveal(),
		trace.WithAttributes(
			attribute.String(telemetry.UIMessageAttr, "received"),
			attribute.String(telemetry.UIActorEmojiAttr, "📝"),
		))
	defer telemetry.End(span, func() error { return nil })

	stdio := telemetry.SpanStdio(ctx, "", log.String(telemetry.ContentTypeAttr, "text/markdown"))

	var sb strings.Builder
	w := io.MultiWriter(&sb, stdio.Stdout)
	fmt.Fprintf(w, "# Comparison Report\n\n")
	fmt.Fprintf(w, "| Model | Eval | Success Rate | Total Attempts | Input Tokens/Attempt | Output Tokens/Attempt |\n")
	fmt.Fprintf(w, "|-------|------|-------------|----------------|---------------------|----------------------|\n")

	// Compare data for each model+eval pair
	for _, modelEval := range slices.Sorted(maps.Keys(afterAggregates)) {
		afterStats := afterAggregates[modelEval]
		beforeStats, exists := beforeAggregates[modelEval]
		if !exists {
			// Skip if we don't have before data for comparison
			continue
		}

		parts := strings.Split(modelEval, ":")
		model, eval := parts[0], parts[1]

		// Format success rate comparison
		successRateComparison := formatComparison(
			beforeStats.successRate*100,
			afterStats.successRate*100,
			true, // Higher is better for success rate
			"%0.0f%%",
		)

		// Format attempts comparison
		attemptsComparison := formatComparison(
			float64(beforeStats.totalAttempts),
			float64(afterStats.totalAttempts),
			false, // Lower is better for attempts
			"%0.0f",
		)

		// Format input tokens comparison
		inputTokensComparison := formatComparison(
			beforeStats.inputTokensPerAttempt,
			afterStats.inputTokensPerAttempt,
			false, // Lower is better for tokens
			"%.1f",
		)

		// Format output tokens comparison
		outputTokensComparison := formatComparison(
			beforeStats.outputTokensPerAttempt,
			afterStats.outputTokensPerAttempt,
			false, // Lower is better for tokens
			"%.1f",
		)

		// Add row to table
		fmt.Fprintf(w, "| `%s` | %s | %s | %s | %s | %s |\n",
			model, eval,
			successRateComparison,
			attemptsComparison,
			inputTokensComparison,
			outputTokensComparison,
		)
	}

	_, err = m.llm().
		WithEnv(dag.Env().
			WithStringInput("table", sb.String(), "The report table.")).
		WithPrompt("Analyze the report.").
		Sync(ctx)
	return err
}

// Helper function to parse CSV data
func parseCSVData(ctx context.Context, file *dagger.File) ([][]string, error) {
	content, err := file.Contents(ctx)
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(strings.NewReader(content))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	return records, nil
}

// Stats struct to hold aggregated data
type aggregateStats struct {
	successRate            float64
	totalAttempts          int
	inputTokensPerAttempt  float64
	outputTokensPerAttempt float64
}

// Helper function to aggregate data
func aggregateData(records [][]string) map[string]aggregateStats {
	aggregates := make(map[string]map[string][]float64)

	headerIndices := make(map[string]int)
	for i, row := range records {
		if i == 0 {
			for j := 0; j < len(row); j++ {
				header := row[j]
				headerIndices[header] = j
			}
			continue
		}
		if len(row) < 6 {
			continue
		}

		model := row[headerIndices["model"]]
		eval := row[headerIndices["eval"]]
		key := model + ":" + eval

		inputTokens, _ := strconv.Atoi(row[headerIndices["input_tokens"]])
		outputTokens, _ := strconv.Atoi(row[headerIndices["output_tokens"]])
		attempts, _ := strconv.Atoi(row[headerIndices["total_attempts"]])
		successRate, _ := strconv.ParseFloat(row[headerIndices["success_rate"]], 64)

		if aggregates[key] == nil {
			aggregates[key] = make(map[string][]float64)
		}

		aggregates[key]["successRate"] = append(aggregates[key]["successRate"], successRate)
		aggregates[key]["attempts"] = append(aggregates[key]["attempts"], float64(attempts))
		aggregates[key]["inputTokens"] = append(aggregates[key]["inputTokens"], float64(inputTokens))
		aggregates[key]["outputTokens"] = append(aggregates[key]["outputTokens"], float64(outputTokens))
	}

	// Calculate final aggregates
	result := make(map[string]aggregateStats)
	for key, values := range aggregates {
		stats := aggregateStats{}

		// Average success rate
		sum := 0.0
		for _, v := range values["successRate"] {
			sum += v
		}
		stats.successRate = sum / float64(len(values["successRate"]))

		// Sum attempts
		totalAttempts := 0
		for _, v := range values["attempts"] {
			totalAttempts += int(v)
		}
		stats.totalAttempts = totalAttempts

		// Tokens per attempt
		totalInputTokens := 0.0
		for _, v := range values["inputTokens"] {
			totalInputTokens += v
		}
		totalOutputTokens := 0.0
		for _, v := range values["outputTokens"] {
			totalOutputTokens += v
		}

		if totalAttempts > 0 {
			stats.inputTokensPerAttempt = totalInputTokens / float64(totalAttempts)
			stats.outputTokensPerAttempt = totalOutputTokens / float64(totalAttempts)
		}

		result[key] = stats
	}

	return result
}

func formatComparison(before, after float64, higherIsBetter bool, format string) string {
	if before == after {
		return fmt.Sprintf(format, before)
	}
	var delta string
	if after > before {
		delta = fmt.Sprintf("+"+format, (after - before))
	} else {
		delta = fmt.Sprintf(format, (after - before))
	}
	return fmt.Sprintf(
		"%s → %s (%s)",
		fmt.Sprintf(format, before),
		fmt.Sprintf(format, after),
		delta,
	)
}
