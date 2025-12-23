package internal

import (
	"testing"

	"github.com/manisharma/k8s-log-streamer/pkg/common"
)

func TestServer_meetsCriteria(t *testing.T) {
	tests := []struct {
		name    string
		cfg     common.LogsStreamerConfig
		options []Option
		raw     []byte
		want    bool
	}{
		{
			name: "match_5xx_with_following_keywords_error_failed",
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"4xx", "5xx", "error", "failed", "panic"},
			},
			options: nil,
			raw:     []byte(`HTTP/1.1 500 Internal Server Error - something failed unexpectedly`),
			want:    true,
		},
		{
			name: "do_not_match_5xx_with_following_keywords_error_failed",
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"4xx", "5xx", "panic"},
			},
			options: nil,
			raw:     []byte(`{"msg": "hello world", "resourceIds": ["500", "123450045"]}`),
			want:    false,
		},
		{
			name: "match_4xx_with_following_keywords_error_failed",
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"4xx", "5xx", "error", "failed", "panic"},
			},
			options: nil,
			raw:     []byte(`HTTP/1.1 400 Bad Request - something failed unexpectedly`),
			want:    true,
		},
		{
			name: "do_not_match_4xx_with_following_keywords_error_failed",
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"4xx", "5xx", "panic"},
			},
			options: nil,
			raw:     []byte(`{"msg": "hello world", "resourceIds": ["404", "123440045"]}`),
			want:    false,
		},
		{
			name: "or_operator_single_match",
			raw:  []byte("This is an error message"),
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error", "warning"},
			},
			options: nil,
			want:    true,
		},
		{
			name: "or_operator_no_match",
			raw:  []byte("This is a normal log message"),
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error", "warning"},
			},
			want: false,
		},
		{
			name: "and_operator_all_match",
			raw:  []byte("database error occurred"),
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"error", "database"},
			},
			want: true,
		},
		{
			name: "and_operator_with_5xx",
			raw:  []byte("HTTP 500 internal server error"),
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"5xx", "error"},
			},
			want: true,
		},
		{
			name: "and_operator_with_4xx",
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"4xx", "notfound"},
			},
			raw:  []byte("HTTP 404 page notfound"),
			want: true,
		},
		{
			name: "empty_input",
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error"},
			},
			raw:  []byte(""),
			want: false,
		},
		{
			name: "large_input",
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error"},
			},
			raw:  []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit. Error occurred in the middle of this very long log message that contains lots of text."),
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer(tt.cfg, tt.options...)
			got := s.meetsCriteria(tt.raw)
			if got != tt.want {
				t.Errorf("meetsCriteria() = %v, want %v", got, tt.want)
			}
		})
	}
}

func BenchmarkServer_meetsCriteria(b *testing.B) {
	tests := []struct {
		name     string
		operator string
		keywords []string
		input    []byte
		cfg      common.LogsStreamerConfig
	}{
		{
			name:  "or_operator_single_match",
			input: []byte("This is an error message"),
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error", "warning"},
			},
		},
		{
			name:  "or_operator_no_match",
			input: []byte("This is a normal log message"),
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error", "warning"},
			},
		},
		{
			name:  "and_operator_all_match",
			input: []byte("database error occurred"),
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"error", "database"},
			},
		},
		{
			name:  "and_operator_with_5xx",
			input: []byte("HTTP 500 internal server error"),
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"5xx", "error"},
			},
		},
		{
			name: "and_operator_with_4xx",
			cfg: common.LogsStreamerConfig{
				Operator: "and",
				Keywords: []string{"4xx", "notfound"},
			},
			input: []byte("HTTP 404 page notfound"),
		},
		{
			name: "empty_input",
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error"},
			},
			input: []byte(""),
		},
		{
			name: "large_input",
			cfg: common.LogsStreamerConfig{
				Operator: "or",
				Keywords: []string{"error"},
			},
			input: []byte("Lorem ipsum dolor sit amet, consectetur adipiscing elit. Error occurred in the middle of this very long log message that contains lots of text."),
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			s := NewServer(tt.cfg)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.meetsCriteria(tt.input)
			}
		})
	}
}
