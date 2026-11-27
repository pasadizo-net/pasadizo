package option

import "github.com/sagernet/sing/common/json/badoption"

type SelectorOutboundOptions struct {
	Outbounds                 []string `json:"outbounds" reference:"outbound"`
	Default                   string   `json:"default,omitempty" reference:"outbound"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections,omitempty"`
}

type URLTestOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds" reference:"outbound"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Tolerance                 uint16             `json:"tolerance,omitempty"`
	Timeout                   badoption.Duration `json:"timeout,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}

type LoadBalanceOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds" reference:"outbound"`
	Strategy                  string             `json:"strategy,omitempty"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Timeout                   badoption.Duration `json:"timeout,omitempty"`
	FailureThreshold          uint16             `json:"failure_threshold,omitempty"`
	FailureWindow             badoption.Duration `json:"failure_window,omitempty"`
	FirstByteTimeout          badoption.Duration `json:"first_byte_timeout,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}

type FallbackOutboundOptions struct {
	Outbounds                 []string           `json:"outbounds" reference:"outbound"`
	URL                       string             `json:"url,omitempty"`
	Interval                  badoption.Duration `json:"interval,omitempty"`
	Timeout                   badoption.Duration `json:"timeout,omitempty"`
	IdleTimeout               badoption.Duration `json:"idle_timeout,omitempty"`
	RecoverAfter              uint16             `json:"recover_after,omitempty"`
	Lazy                      []string           `json:"lazy,omitempty" reference:"outbound"`
	InterruptExistConnections bool               `json:"interrupt_exist_connections,omitempty"`
}
