package orgsdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var identifierPattern = regexp.MustCompile("^[a-z0-9](?:[a-z0-9._/-]*[a-z0-9])?$")
var permissionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9:._-]{0,127}$`)

type NodeType string

const (
	NodeTypeActivity      NodeType = "activity"
	NodeTypeWaitForAction NodeType = "wait-for-action"
	NodeTypeSemantic      NodeType = "semantic"
)

type Cardinality string

const (
	CardinalitySingleton Cardinality = "singleton"
	CardinalityRepeated  Cardinality = "repeated"
)

type SideEffect string

const (
	SideEffectNone  SideEffect = "none"
	SideEffectRead  SideEffect = "read"
	SideEffectWrite SideEffect = "write"
)

type RetryPolicy struct {
	InitialInterval     time.Duration `json:"initialInterval"`
	BackoffCoefficient  float64       `json:"backoffCoefficient"`
	MaximumInterval     time.Duration `json:"maximumInterval"`
	MaximumAttempts     int32         `json:"maximumAttempts"`
	StartToCloseTimeout time.Duration `json:"startToCloseTimeout"`
}

type IdempotencyPolicy struct {
	BusinessKeyRequired bool   `json:"businessKeyRequired"`
	PropagationField    string `json:"propagationField,omitempty"`
}

type ActivityPolicy struct {
	SideEffect     SideEffect         `json:"sideEffect"`
	Retry          RetryPolicy        `json:"retryPolicy"`
	Idempotency    *IdempotencyPolicy `json:"idempotency,omitempty"`
	Reconciliation string             `json:"reconciliation,omitempty"`
	Compensation   string             `json:"compensation,omitempty"`
}

type ActionDefinition struct {
	Name               string          `json:"name"`
	Label              string          `json:"label"`
	NodeTemplateID     string          `json:"nodeTemplateId,omitempty"`
	RequiredPermission string          `json:"requiredPermission"`
	InputSchema        json.RawMessage `json:"inputSchema,omitempty"`
}

type NodeTemplate struct {
	ID           string             `json:"id"`
	Label        string             `json:"label"`
	Type         NodeType           `json:"type"`
	Cardinality  Cardinality        `json:"cardinality,omitempty"`
	InputSchema  json.RawMessage    `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage    `json:"outputSchema,omitempty"`
	Activity     *ActivityPolicy    `json:"activity,omitempty"`
	Actions      []ActionDefinition `json:"actions,omitempty"`
}

type RuntimeBounds struct {
	MaxInstancesPerFanOut int `json:"maxInstancesPerFanOut"`
	MaxRuntimeNodes       int `json:"maxRuntimeNodes"`
	MaxProjectionBytes    int `json:"maxProjectionBytes"`
}

type Definition struct {
	Name         string          `json:"name"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Templates    []NodeTemplate  `json:"nodeTemplates"`
	Bounds       RuntimeBounds   `json:"runtimeBounds"`
}

func NewDefinition[I, O any](name string, templates []NodeTemplate, bounds RuntimeBounds) Definition {
	return Definition{
		Name: name, InputSchema: schemaFor[I](), OutputSchema: schemaFor[O](),
		Templates: append([]NodeTemplate(nil), templates...), Bounds: bounds,
	}
}

func (d Definition) Validate() error {
	var problems []string
	if !identifierPattern.MatchString(d.Name) {
		problems = append(problems, "definition name must be a stable identifier")
	}
	if d.Bounds.MaxInstancesPerFanOut <= 0 || d.Bounds.MaxRuntimeNodes <= 0 || d.Bounds.MaxProjectionBytes <= 0 {
		problems = append(problems, "runtime bounds must be finite positive values")
	}
	seen := make(map[string]bool, len(d.Templates))
	seenActions := map[string]bool{}
	for _, template := range d.Templates {
		if !identifierPattern.MatchString(template.ID) || strings.TrimSpace(template.Label) == "" {
			problems = append(problems, "template ID and label are required")
		}
		if strings.HasPrefix(template.ID, "org.sdk/") {
			problems = append(problems, fmt.Sprintf("template %q uses reserved SDK prefix", template.ID))
		}
		if seen[template.ID] {
			problems = append(problems, fmt.Sprintf("duplicate template %q", template.ID))
		}
		seen[template.ID] = true
		if template.Cardinality != "" && template.Cardinality != CardinalitySingleton && template.Cardinality != CardinalityRepeated {
			problems = append(problems, fmt.Sprintf("template %q has invalid cardinality", template.ID))
		}
		if len(template.InputSchema) > 0 && !json.Valid(template.InputSchema) || len(template.OutputSchema) > 0 && !json.Valid(template.OutputSchema) {
			problems = append(problems, fmt.Sprintf("template %q input or output schema is invalid", template.ID))
		}
		switch template.Type {
		case NodeTypeActivity:
			if template.Activity == nil {
				problems = append(problems, fmt.Sprintf("activity template %q requires a policy", template.ID))
				continue
			}
			policy := template.Activity
			if policy.Retry.MaximumAttempts <= 0 || policy.Retry.StartToCloseTimeout <= 0 {
				problems = append(problems, fmt.Sprintf("activity template %q requires retry attempts and timeout", template.ID))
			}
			if policy.SideEffect != SideEffectNone && policy.SideEffect != SideEffectRead && policy.SideEffect != SideEffectWrite {
				problems = append(problems, fmt.Sprintf("activity template %q has invalid side effect", template.ID))
			}
			if policy.SideEffect == SideEffectWrite && policy.Idempotency == nil && strings.TrimSpace(policy.Reconciliation) == "" {
				problems = append(problems, fmt.Sprintf("write activity %q requires idempotency or reconciliation", template.ID))
			}
			if policy.SideEffect == SideEffectWrite && policy.Idempotency == nil && policy.Retry.MaximumAttempts != 1 {
				problems = append(problems, fmt.Sprintf("write activity %q without idempotency must disable automatic retries", template.ID))
			}
		case NodeTypeWaitForAction:
			if len(template.Actions) == 0 {
				problems = append(problems, fmt.Sprintf("wait template %q requires actions", template.ID))
			}
			for _, action := range template.Actions {
				if !identifierPattern.MatchString(action.Name) || strings.TrimSpace(action.Label) == "" {
					problems = append(problems, fmt.Sprintf("wait template %q action name and label are required", template.ID))
				}
				if !permissionPattern.MatchString(action.RequiredPermission) {
					problems = append(problems, fmt.Sprintf("wait template %q action permission is required", template.ID))
				}
				if len(action.InputSchema) > 0 && !json.Valid(action.InputSchema) {
					problems = append(problems, fmt.Sprintf("wait template %q action %q schema is invalid", template.ID, action.Name))
				}
				if action.NodeTemplateID != "" {
					problems = append(problems, fmt.Sprintf("wait template %q action %q cannot override node template identity", template.ID, action.Name))
				}
				if seenActions[action.Name] {
					problems = append(problems, fmt.Sprintf("duplicate action %q", action.Name))
				}
				seenActions[action.Name] = true
			}
		case NodeTypeSemantic:
		default:
			problems = append(problems, fmt.Sprintf("template %q has invalid type", template.ID))
		}
	}
	if len(d.Templates) == 0 {
		problems = append(problems, "definition must contain templates")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func (d Definition) template(id string) (NodeTemplate, bool) {
	for _, template := range d.Templates {
		if template.ID == id {
			return template, true
		}
	}
	return NodeTemplate{}, false
}
