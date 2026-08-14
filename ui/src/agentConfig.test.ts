import { describe, expect, it } from "vitest";
import { agentConfigInput, draftFromAgentConfig, emptyAgentConfigDraft } from "./agentConfig";

describe("agent execution configuration", () => {
  it("creates and restores empty drafts", () => {
    expect(emptyAgentConfigDraft()).toEqual({
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
    });
    expect(draftFromAgentConfig()).toEqual(emptyAgentConfigDraft());
  });

  it("maps Codex values and trims optional strings", () => {
    const draft = emptyAgentConfigDraft();
    Object.assign(draft, {
      workMode: " IMPLEMENT ",
      codexModel: " gpt-test ",
      codexReasoningEffort: "HIGH",
      codexSandboxMode: "WORKSPACE_WRITE",
      codexApprovalPolicy: "NEVER",
      codexFullAuto: true,
      codexBypassApprovalsAndSandbox: true,
    });
    expect(agentConfigInput("codex", draft)).toEqual({
      workMode: "IMPLEMENT",
      codex: {
        model: "gpt-test",
        reasoningEffort: "HIGH",
        sandboxMode: "WORKSPACE_WRITE",
        approvalPolicy: "NEVER",
        fullAuto: true,
        bypassApprovalsAndSandbox: true,
      },
    });
    expect(draftFromAgentConfig({
      workMode: "IMPLEMENT",
      codex: {
        model: "gpt-test",
        reasoningEffort: "HIGH",
        sandboxMode: "WORKSPACE_WRITE",
        approvalPolicy: "NEVER",
        fullAuto: true,
        bypassApprovalsAndSandbox: true,
      },
    })).toMatchObject({ codexModel: "gpt-test", codexFullAuto: true });
  });

  it("maps Claude values and ignores unrelated agents", () => {
    const draft = emptyAgentConfigDraft();
    Object.assign(draft, {
      claudeModel: "sonnet",
      claudeEffort: "MAX",
      claudePermissionMode: "PLAN",
    });
    expect(agentConfigInput("claude", draft)).toEqual({
      claude: { model: "sonnet", effort: "MAX", permissionMode: "PLAN" },
    });
    expect(agentConfigInput("custom", draft)).toEqual({});
    expect(draftFromAgentConfig({ claude: { model: "sonnet", effort: "MAX", permissionMode: "PLAN" } }))
      .toMatchObject({ claudeModel: "sonnet", claudeEffort: "MAX", claudePermissionMode: "PLAN" });
  });
});
