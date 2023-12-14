// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package filterprocessor // import "github.com/christophermancini/filterprocessor"

import (
	"context"

	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processorhelper"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/christophermancini/filterprocessor/internal/filter/expr"
	"github.com/christophermancini/filterprocessor/internal/filter/filterottl"
	"github.com/christophermancini/filterprocessor/internal/filter/filterspan"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl/contexts/ottlspan"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/ottl/contexts/ottlspanevent"
)

type filterSpanProcessor struct {
	skipSpanExpr      expr.BoolExpr[ottlspan.TransformContext]
	skipSpanEventExpr expr.BoolExpr[ottlspanevent.TransformContext]
	logger            *zap.Logger
}

func newFilterSpansProcessor(set component.TelemetrySettings, cfg *Config) (*filterSpanProcessor, error) {
	var err error
	fsp := &filterSpanProcessor{
		logger: set.Logger,
	}
	if cfg.Traces.SpanConditions != nil || cfg.Traces.SpanEventConditions != nil {
		if cfg.Traces.SpanConditions != nil {
			fsp.skipSpanExpr, err = filterottl.NewBoolExprForSpan(cfg.Traces.SpanConditions, filterottl.StandardSpanFuncs(), cfg.ErrorMode, set)
			if err != nil {
				return nil, err
			}
		}
		if cfg.Traces.SpanEventConditions != nil {
			fsp.skipSpanEventExpr, err = filterottl.NewBoolExprForSpanEvent(cfg.Traces.SpanEventConditions, filterottl.StandardSpanEventFuncs(), cfg.ErrorMode, set)
			if err != nil {
				return nil, err
			}
		}
		return fsp, nil
	}

	fsp.skipSpanExpr, err = filterspan.NewSkipExpr(&cfg.Spans)
	if err != nil {
		return nil, err
	}

	includeMatchType, excludeMatchType := "[None]", "[None]"
	if cfg.Spans.Include != nil {
		includeMatchType = string(cfg.Spans.Include.MatchType)
	}

	if cfg.Spans.Exclude != nil {
		excludeMatchType = string(cfg.Spans.Exclude.MatchType)
	}

	set.Logger.Info(
		"Span filter configured",
		zap.String("[Include] match_type", includeMatchType),
		zap.String("[Exclude] match_type", excludeMatchType),
	)

	return fsp, nil
}

// processTraces filters the given spans of a traces based off the filterSpanProcessor's filters.
func (fsp *filterSpanProcessor) processTraces(ctx context.Context, td ptrace.Traces) (ptrace.Traces, error) {
	if fsp.skipSpanExpr == nil && fsp.skipSpanEventExpr == nil {
		return td, nil
	}

	var errors error
	td.ResourceSpans().RemoveIf(func(rs ptrace.ResourceSpans) bool {
		resource := rs.Resource()
		rs.ScopeSpans().RemoveIf(func(ss ptrace.ScopeSpans) bool {
			scope := ss.Scope()
			ss.Spans().RemoveIf(func(span ptrace.Span) bool {
				if fsp.skipSpanExpr != nil {
					skip, err := fsp.skipSpanExpr.Eval(ctx, ottlspan.NewTransformContext(span, scope, resource))
					if err != nil {
						errors = multierr.Append(errors, err)
						return false
					}
					if skip {
						return true
					}
				}
				if fsp.skipSpanEventExpr != nil {
					span.Events().RemoveIf(func(spanEvent ptrace.SpanEvent) bool {
						skip, err := fsp.skipSpanEventExpr.Eval(ctx, ottlspanevent.NewTransformContext(spanEvent, span, scope, resource))
						if err != nil {
							errors = multierr.Append(errors, err)
							return false
						}
						return skip
					})
				}
				return false
			})
			return ss.Spans().Len() == 0
		})
		return rs.ScopeSpans().Len() == 0
	})

	if errors != nil {
		fsp.logger.Error("failed processing traces", zap.Error(errors))
		return td, errors
	}
	if td.ResourceSpans().Len() == 0 {
		return td, processorhelper.ErrSkipProcessingData
	}
	return td, nil
}
