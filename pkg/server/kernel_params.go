package server

import (
	"os"
	"runtime"
	"strings"

	"go.uber.org/zap"
)

type kernelParamCheck struct {
	name     string
	expected string
}

var (
	kernelParamChecks = []kernelParamCheck{
		{name: "net.ipv4.ip_forward", expected: "1"},
		{name: "net.ipv4.vs.conntrack", expected: "1"},
		{name: "net.ipv4.conf.all.rp_filter", expected: "0"},
		{name: "net.ipv4.conf.default.rp_filter", expected: "0"},
	}
	kernelParamCheckEnabled = runtime.GOOS == "linux"
	readKernelParamFile     = os.ReadFile
)

func (s *Server) logKernelParamPreflight() {
	if !kernelParamCheckEnabled || s.logger == nil {
		return
	}

	allMatched := true
	for _, check := range kernelParamChecks {
		raw, err := readKernelParamFile(kernelParamPath(check.name))
		if err != nil {
			allMatched = false
			s.logger.Error("failed to read kernel parameter",
				zap.String("name", check.name),
				zap.Error(err),
			)
			continue
		}

		actual := strings.TrimSpace(string(raw))
		if actual == check.expected {
			continue
		}

		allMatched = false
		s.logger.Error("kernel parameter mismatch",
			zap.String("name", check.name),
			zap.String("expected", check.expected),
			zap.String("actual", actual),
		)
	}

	if allMatched {
		s.logger.Info("kernel parameter preflight passed")
	}
}

func kernelParamPath(name string) string {
	return "/proc/sys/" + strings.ReplaceAll(name, ".", "/")
}
