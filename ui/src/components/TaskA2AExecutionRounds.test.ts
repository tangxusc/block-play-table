import { mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import TaskA2AExecutionRounds from "./TaskA2AExecutionRounds.vue";
import type { TaskA2AExecution } from "../models";

const baseExecution: TaskA2AExecution = {
  id: "round-1",
  executionId: "execution-1",
  attempt: 1,
  turn: 2,
  operation: "CONTINUE",
  workerId: "worker-1",
  a2aTaskId: "a2a-task-12345678901234567890",
  contextId: "context-12345678901234567890",
  remoteStatus: "WORKING",
  lastSequence: 7,
  lastSyncedAt: "2026-08-10T10:20:30Z",
  errorCode: null,
  errorMessage: null,
  retryable: false,
  createdAt: "2026-08-10T10:00:00Z",
  completedAt: null,
};

function mountRounds(executions: TaskA2AExecution[]) {
  return mount(TaskA2AExecutionRounds, {
    props: { executions },
    global: {
      stubs: {
        "a-tag": { template: "<span><slot /></span>" },
        "a-empty": { props: ["description"], template: "<div>{{ description }}</div>" },
      },
    },
  });
}

describe("TaskA2AExecutionRounds", () => {
  it("renders an empty state", () => {
    const wrapper = mountRounds([]);
    expect(wrapper.text()).toContain("No A2A execution rounds");
    expect(wrapper.findAll('[data-testid="a2a-execution-round"]')).toHaveLength(0);
  });

  it("renders compact identities, status and sync metadata", () => {
    const wrapper = mountRounds([baseExecution]);
    expect(wrapper.findAll('[data-testid="a2a-execution-round"]')).toHaveLength(1);
    expect(wrapper.text()).toContain("#1 / 2");
    expect(wrapper.text()).toContain("CONTINUE");
    expect(wrapper.text()).toContain("WORKING");
    expect(wrapper.text()).toContain("a2a-task-1...4567890");
    expect(wrapper.find("time").attributes("datetime")).toBe(baseExecution.lastSyncedAt);
  });

  it("renders missing, invalid and failed round values", () => {
    const failed = {
      ...baseExecution,
      id: "round-2",
      a2aTaskId: null,
      contextId: "short-context",
      lastSyncedAt: "invalid-time",
      errorCode: "A2A_TIMEOUT",
      errorMessage: "stream stopped",
      retryable: true,
    };
    const messageOnly = { ...failed, id: "round-3", errorCode: null, errorMessage: "failed" };
    const wrapper = mountRounds([failed, messageOnly]);
    expect(wrapper.text()).toContain("invalid-time");
    expect(wrapper.text()).toContain("A2A_TIMEOUT: stream stopped (retryable)");
    expect(wrapper.text()).toContain("short-context");
    expect(wrapper.findAll(".a2a-round-error")).toHaveLength(2);
  });
});
