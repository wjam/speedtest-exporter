package internal

import (
	"fmt"
	"log/slog"
)

var _ slog.LogValuer = stringerValue{}

type stringerValue struct {
	value fmt.Stringer
}

func (s stringerValue) LogValue() slog.Value {
	return slog.StringValue(s.value.String())
}
