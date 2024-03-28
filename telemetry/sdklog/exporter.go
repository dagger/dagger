package sdklog

import "context"

type LogExporter interface {
	ExportLogs(ctx context.Context, logs []*LogData) error
	Shutdown(ctx context.Context) error
}
