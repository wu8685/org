package orgsdk

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

const (
	ContractVersion        = "org.worker/v1"
	SDKModuleVersion       = "0.1.0"
	RuntimeProtocolVersion = "1"
	ProjectionEventVersion = "1"
	DynamicNodeIDVersion   = "1"
)

type SDKManifest struct {
	ModuleVersion          string `json:"moduleVersion"`
	RuntimeProtocolVersion string `json:"runtimeProtocolVersion"`
}

type WorkflowManifest struct {
	Name               string             `json:"name"`
	VersioningBehavior string             `json:"versioningBehavior"`
	ProjectionQuery    string             `json:"projectionQuery"`
	InputSchema        json.RawMessage    `json:"inputSchema,omitempty"`
	OutputSchema       json.RawMessage    `json:"outputSchema,omitempty"`
	NodeTemplates      []NodeTemplate     `json:"nodeTemplates"`
	Actions            []ActionDefinition `json:"actions,omitempty"`
	RuntimeBounds      RuntimeBounds      `json:"runtimeBounds"`
}

type Manifest struct {
	ContractVersion        string             `json:"contractVersion"`
	ProjectionEventVersion string             `json:"projectionEventVersion"`
	DynamicNodeIDVersion   string             `json:"dynamicNodeIdVersion"`
	SDK                    SDKManifest        `json:"sdk"`
	Workflows              []WorkflowManifest `json:"workflows"`
	Activities             []ActivityManifest `json:"activities"`
}

type ActivityManifest struct {
	Name         string          `json:"name"`
	InputSchema  json.RawMessage `json:"inputSchema,omitempty"`
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"`
	Policy       ActivityPolicy  `json:"policy"`
}

func GenerateManifest(workflowName string, definition Definition) ([]byte, string, error) {
	if workflowName == "" {
		return nil, "", errors.New("workflow name is required")
	}
	if err := definition.Validate(); err != nil {
		return nil, "", err
	}
	manifest := Manifest{
		ContractVersion:        ContractVersion,
		ProjectionEventVersion: ProjectionEventVersion,
		DynamicNodeIDVersion:   DynamicNodeIDVersion,
		SDK: SDKManifest{
			ModuleVersion:          SDKModuleVersion,
			RuntimeProtocolVersion: RuntimeProtocolVersion,
		},
		Workflows: []WorkflowManifest{{
			Name: workflowName, VersioningBehavior: "pinned", ProjectionQuery: ReservedProjectionQuery,
			InputSchema: definition.InputSchema, OutputSchema: definition.OutputSchema,
			NodeTemplates: append([]NodeTemplate(nil), definition.Templates...), Actions: manifestActions(definition),
			RuntimeBounds: definition.Bounds,
		}},
		Activities: manifestActivities(definition),
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(encoded)
	return encoded, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func manifestActivities(definition Definition) []ActivityManifest {
	activities := make([]ActivityManifest, 0)
	for _, template := range definition.Templates {
		if template.Type != NodeTypeActivity || template.Activity == nil {
			continue
		}
		activities = append(activities, ActivityManifest{
			Name: template.ID, InputSchema: append(json.RawMessage(nil), template.InputSchema...),
			OutputSchema: append(json.RawMessage(nil), template.OutputSchema...), Policy: *cloneActivityPolicy(*template.Activity),
		})
	}
	return activities
}

func manifestActions(definition Definition) []ActionDefinition {
	var actions []ActionDefinition
	for _, template := range definition.Templates {
		for _, action := range template.Actions {
			action.NodeTemplateID = template.ID
			actions = append(actions, action)
		}
	}
	return actions
}
