import type { AgentExecutionConfig } from "./models";

export interface AgentConfigDraft {
  workMode: string;
  codexModel: string;
  codexReasoningEffort: string;
  codexSandboxMode: string;
  codexApprovalPolicy: string;
  codexFullAuto: boolean;
  codexBypassApprovalsAndSandbox: boolean;
  claudeModel: string;
  claudeEffort: string;
  claudePermissionMode: string;
}

export function emptyAgentConfigDraft(): AgentConfigDraft {
  return {
    workMode: "",
    codexModel: "",
    codexReasoningEffort: "",
    codexSandboxMode: "",
    codexApprovalPolicy: "",
    codexFullAuto: false,
    codexBypassApprovalsAndSandbox: false,
    claudeModel: "",
    claudeEffort: "",
    claudePermissionMode: "",
  };
}

export function draftFromAgentConfig(config?: AgentExecutionConfig): AgentConfigDraft {
  return {
    workMode: config?.workMode || "",
    codexModel: config?.codex?.model || "",
    codexReasoningEffort: config?.codex?.reasoningEffort || "",
    codexSandboxMode: config?.codex?.sandboxMode || "",
    codexApprovalPolicy: config?.codex?.approvalPolicy || "",
    codexFullAuto: Boolean(config?.codex?.fullAuto),
    codexBypassApprovalsAndSandbox: Boolean(config?.codex?.bypassApprovalsAndSandbox),
    claudeModel: config?.claude?.model || "",
    claudeEffort: config?.claude?.effort || "",
    claudePermissionMode: config?.claude?.permissionMode || "",
  };
}

export function agentConfigInput(agentType: string, draft: AgentConfigDraft): Record<string, unknown> {
  const input: Record<string, unknown> = {};
  putIfPresent(input, "workMode", draft.workMode);
  if (agentType === "codex") {
    const codex: Record<string, unknown> = {
      fullAuto: draft.codexFullAuto,
      bypassApprovalsAndSandbox: draft.codexBypassApprovalsAndSandbox,
    };
    putIfPresent(codex, "model", draft.codexModel);
    putIfPresent(codex, "reasoningEffort", draft.codexReasoningEffort);
    putIfPresent(codex, "sandboxMode", draft.codexSandboxMode);
    putIfPresent(codex, "approvalPolicy", draft.codexApprovalPolicy);
    input.codex = codex;
  }
  if (agentType === "claude") {
    const claude: Record<string, unknown> = {};
    putIfPresent(claude, "model", draft.claudeModel);
    putIfPresent(claude, "effort", draft.claudeEffort);
    putIfPresent(claude, "permissionMode", draft.claudePermissionMode);
    input.claude = claude;
  }
  return input;
}

function putIfPresent(target: Record<string, unknown>, key: string, value: string) {
  const trimmed = value.trim();
  if (trimmed) target[key] = trimmed;
}
