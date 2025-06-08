package telemetry

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/denysvitali/openhands-runtime-go/pkg/config"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otlplog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/global"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
)

// Initialize sets up OpenTelemetry tracing and logging using autoexport
func Initialize(cfg config.TelemetryConfig, logger *logrus.Logger) (func(), error) {
	// Create resource
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("openhands-runtime"),
			semconv.ServiceVersionKey.String("1.0.0"),
		),
	)
	if err != nil {
		return nil, err
	}

	// Initialize trace provider using autoexport
	traceShutdown, err := autoexport.NewSpanExporter(context.Background())
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceShutdown),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// Set global trace provider
	otel.SetTracerProvider(tp)

	// Initialize log provider using autoexport
	logExporter, err := autoexport.NewLogExporter(context.Background())
	if err != nil {
		logger.Warnf("Failed to create log exporter: %v", err)
	}

	var logProvider *sdklog.LoggerProvider
	if logExporter != nil {
		logProvider = sdklog.NewLoggerProvider(
			sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
			sdklog.WithResource(res),
		)
		global.SetLoggerProvider(logProvider)
	}

	// Set global propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Return cleanup function
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("Error shutting down tracer provider: %v", err)
		}

		if logProvider != nil {
			if err := logProvider.Shutdown(ctx); err != nil {
				log.Printf("Error shutting down log provider: %v", err)
			}
		}
	}, nil
}

// ReportJSON reports the given data as JSON in both traces and logs (debug level)
func ReportJSON(ctx context.Context, logger *logrus.Logger, operationName string, data interface{}) {
	// Convert data to JSON
	jsonData, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("Failed to marshal data to JSON: %v", err)
		return
	}

	// Report in trace
	ReportJSONInTrace(ctx, operationName, data, jsonData)

	// Report in logs (debug level)
	ReportJSONInLogs(logger, operationName, data, jsonData)
}

// ReportJSONInTrace adds JSON data to the current trace span
func ReportJSONInTrace(ctx context.Context, operationName string, data interface{}, jsonData []byte) {
	tracer := otel.Tracer("openhands-runtime")
	_, span := tracer.Start(ctx, operationName)
	defer span.End()

	// Add JSON as span attribute
	span.SetAttributes(
		attribute.String("json.data", string(jsonData)),
		attribute.String("data.type", getDataType(data)),
	)

	// Add individual fields if it's a map
	if dataMap, ok := data.(map[string]interface{}); ok {
		for key, value := range dataMap {
			if strValue, ok := value.(string); ok {
				span.SetAttributes(attribute.String("data."+key, strValue))
			} else if intValue, ok := value.(int); ok {
				span.SetAttributes(attribute.Int("data."+key, intValue))
			} else if floatValue, ok := value.(float64); ok {
				span.SetAttributes(attribute.Float64("data."+key, floatValue))
			} else if boolValue, ok := value.(bool); ok {
				span.SetAttributes(attribute.Bool("data."+key, boolValue))
			}
		}
	}
}

// ReportJSONInLogs logs JSON data at debug level
func ReportJSONInLogs(logger *logrus.Logger, operationName string, data interface{}, jsonData []byte) {
	logger.WithFields(logrus.Fields{
		"operation": operationName,
		"json_data": string(jsonData),
		"data_type": getDataType(data),
	}).Debug("JSON data reported")

	// Also send to OpenTelemetry logs if available
	otelLogger := global.GetLoggerProvider().Logger("openhands-runtime")
	if otelLogger != nil {
		var record otlplog.Record
		record.SetTimestamp(time.Now())
		record.SetObservedTimestamp(time.Now())
		record.SetSeverity(otlplog.SeverityDebug)
		record.SetSeverityText("DEBUG")
		record.SetBody(otlplog.StringValue(string(jsonData)))
		record.AddAttributes(
			otlplog.String("operation", operationName),
			otlplog.String("data_type", getDataType(data)),
		)
		otelLogger.Emit(context.Background(), record)
	}
}

// getDataType returns a string representation of the data type
func getDataType(data interface{}) string {
	switch data.(type) {
	case map[string]interface{}:
		return "map"
	case []interface{}:
		return "array"
	case string:
		return "string"
	case int, int32, int64:
		return "integer"
	case float32, float64:
		return "float"
	case bool:
		return "boolean"
	default:
		return "unknown"
	}
}
