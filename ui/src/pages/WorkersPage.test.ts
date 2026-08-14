import { flushPromises, shallowMount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DomainEventItem, ProjectItem, WorkerItem } from "../models";
import WorkersPage from "./WorkersPage.vue";

const apiMocks = vi.hoisted(() => ({
  subscribeDomainEvents: vi.fn(),
  fetchWorkersPage: vi.fn(),
  fetchProjects: vi.fn(),
  updateWorker: vi.fn(),
  enableWorker: vi.fn(),
  disableWorker: vi.fn(),
  workerTerminalCheckUrlForWorker: vi.fn((id: string) => `/terminal/${id}`),
  workerTerminalWebSocketUrlForWorker: vi.fn((id: string) => `ws://terminal/${id}`),
}));

vi.mock("../api", () => ({ api: apiMocks }));

const project: ProjectItem = {
  id: "project-1",
  name: "Project One",
  gitUrl: "repo",
  defaultBranch: "main",
  worktreeNamePrefix: "task",
  archived: false,
  createdAt: "now",
  updatedAt: "now",
};

const worker: WorkerItem = {
  id: "worker-1",
  name: "Worker One",
  status: "ONLINE",
  capabilities: [],
  supportedAgents: ["codex", "claude"],
  workDir: "/workspace",
  projectBindingMode: "SPECIFIC_PROJECTS",
  boundProjectIds: [project.id],
  agentRuntimeEnv: [
    {
      agentType: "codex",
      vars: [
        { key: "PLAIN", value: "visible", description: "plain", enabled: true, sensitive: false },
        { key: "SECRET", valueMasked: "********", enabled: false, sensitive: true },
      ],
    },
  ],
  currentTaskIds: ["task-1"],
  createdAt: "now",
  updatedAt: "now",
};

const domainEvent = (aggregateType: string): DomainEventItem => ({
  eventId: `event-${aggregateType}`,
  eventType: `${aggregateType}Updated`,
  aggregateType,
  aggregateId: "id",
  aggregateVersion: 1,
  payload: "{}",
  occurredAt: "now",
});

describe("WorkersPage", () => {
  let subscriber: (event: DomainEventItem) => void;
  let unsubscribe: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    unsubscribe = vi.fn();
    apiMocks.subscribeDomainEvents.mockImplementation((callback: (event: DomainEventItem) => void) => {
      subscriber = callback;
      return unsubscribe;
    });
    apiMocks.fetchWorkersPage.mockResolvedValue({ items: [worker], totalCount: 1 });
    apiMocks.fetchProjects.mockResolvedValue([project]);
    apiMocks.updateWorker.mockResolvedValue(worker);
    apiMocks.enableWorker.mockResolvedValue(undefined);
    apiMocks.disableWorker.mockResolvedValue(undefined);
  });

  it("loads data, follows filters and refresh events, then unsubscribes", async () => {
    const wrapper = shallowMount(WorkersPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as {
      data: { items: WorkerItem[] };
      projects: ProjectItem[];
      search: string;
      pageReq: { offset: number; limit: number };
    };
    expect(vm.data.items[0].name).toBe("Worker One");
    expect(vm.projects).toEqual([project]);
    subscriber(domainEvent("Settings"));
    subscriber(domainEvent("Worker"));
    subscriber(domainEvent("Project"));
    vm.search = "worker";
    await wrapper.vm.$nextTick();
    vm.pageReq = { offset: 20, limit: 20 };
    await wrapper.vm.$nextTick();
    await flushPromises();
    expect(apiMocks.fetchWorkersPage.mock.calls.length).toBeGreaterThanOrEqual(4);
    wrapper.unmount();
    expect(unsubscribe).toHaveBeenCalled();
  });

  it("opens the startup flow and executes Worker card actions", async () => {
    const wrapper = shallowMount(WorkersPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as {
      draft: { workerId: string };
      formDialogOpen: boolean;
      commandDialogOpen: boolean;
      terminalWorker: WorkerItem | null;
      editDraft: WorkerItem | null;
      openNewWorkerForm: () => void;
      generateCommand: () => void;
    };
    vm.draft.workerId = "changed";
    vm.openNewWorkerForm();
    expect(vm.draft.workerId).toBe("worker-local");
    expect(vm.formDialogOpen).toBe(true);
    expect(wrapper.findAllComponents({ name: "ACheckboxGroup" })).toHaveLength(0);
    vm.generateCommand();
    expect(vm.formDialogOpen).toBe(false);
    expect(vm.commandDialogOpen).toBe(true);

    const buttons = wrapper.findAllComponents({ name: "AButton" });
    const click = (label: string) => buttons.find((button) => button.attributes("aria-label") === label)!.vm.$emit("click");
    click("Open worker terminal");
    click("Edit worker");
    click("Enable worker");
    click("Disable worker");
    await flushPromises();
    expect(vm.terminalWorker?.id).toBe(worker.id);
    expect(vm.editDraft?.id).toBe(worker.id);
    expect(apiMocks.enableWorker).toHaveBeenCalledWith(worker.id);
    expect(apiMocks.disableWorker).toHaveBeenCalledWith(worker.id);
  });

  it("clones, edits and saves Worker runtime environment groups", async () => {
    const wrapper = shallowMount(WorkersPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as {
      editDraft: WorkerItem | null;
      editDialogOpen: boolean;
      activeAgent: "codex" | "claude";
      activeEnvVars: WorkerItem["agentRuntimeEnv"][number]["vars"];
      envDraft: WorkerItem["agentRuntimeEnv"][number]["vars"][number];
      envDialogOpen: boolean;
      editingEnvKey: string;
      openEditWorker: (value: WorkerItem) => void;
      saveEditWorker: () => Promise<void>;
      openEnvDialog: () => void;
      editEnvVar: (value: WorkerItem["agentRuntimeEnv"][number]["vars"][number]) => void;
      saveEnvVar: () => void;
      removeEnvVar: (value: WorkerItem["agentRuntimeEnv"][number]["vars"][number]) => void;
    };
    await expect(vm.saveEditWorker()).resolves.toBeUndefined();
    vm.openEditWorker(worker);
    expect(vm.editDraft).not.toBe(worker);
    expect(vm.editDraft?.supportedAgents).not.toBe(worker.supportedAgents);
    expect(vm.editDraft?.supportedAgents).toEqual(["codex", "claude"]);
    expect(vm.editDraft?.agentRuntimeEnv.map((group) => group.agentType)).toEqual(["codex", "claude"]);
    expect(vm.activeEnvVars).toHaveLength(2);

    vm.openEnvDialog();
    expect(vm.envDraft).toMatchObject({ key: "", enabled: true, sensitive: true });
    vm.saveEnvVar();
    expect(vm.envDialogOpen).toBe(true);
    vm.envDraft = { key: " NEW ", value: " ", description: " description ", enabled: true, sensitive: false };
    vm.saveEnvVar();
    expect(vm.activeEnvVars.find((item) => item.key === "NEW")).toEqual(expect.objectContaining({ description: "description" }));
    expect(vm.activeEnvVars.find((item) => item.key === "NEW")).not.toHaveProperty("value");

    const plain = vm.activeEnvVars.find((item) => item.key === "PLAIN")!;
    vm.editEnvVar(plain);
    expect(vm.envDraft.value).toBe("visible");
    vm.envDraft.value = "changed";
    vm.saveEnvVar();
    expect(vm.activeEnvVars.find((item) => item.key === "PLAIN")?.value).toBe("changed");

    const secret = vm.activeEnvVars.find((item) => item.key === "SECRET")!;
    vm.editEnvVar(secret);
    expect(vm.envDraft.value).toBe("");
    vm.removeEnvVar(secret);
    expect(vm.activeEnvVars.some((item) => item.key === "SECRET")).toBe(false);

    vm.activeAgent = "claude";
    await wrapper.vm.$nextTick();
    expect(vm.activeEnvVars).toEqual([]);
    vm.openEnvDialog();
    vm.envDraft = { key: "CLAUDE_ENV", valueMasked: "masked", enabled: true, sensitive: false };
    vm.saveEnvVar();
    vm.editEnvVar(vm.activeEnvVars[0]);
    expect(vm.envDraft.value).toBe("masked");
    await vm.saveEditWorker();
    expect(apiMocks.updateWorker).toHaveBeenCalledWith(expect.objectContaining({
      id: worker.id,
      supportedAgents: ["codex", "claude"],
    }));
    expect(vm.editDialogOpen).toBe(false);
  });

  it("guards environment operations without an edit draft", async () => {
    const wrapper = shallowMount(WorkersPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as {
      envDraft: WorkerItem["agentRuntimeEnv"][number]["vars"][number];
      activeEnvVars: unknown[];
      saveEnvVar: () => void;
      removeEnvVar: (value: WorkerItem["agentRuntimeEnv"][number]["vars"][number]) => void;
    };
    vm.envDraft = { key: "KEY", value: "value", enabled: true, sensitive: false };
    vm.saveEnvVar();
    vm.removeEnvVar(vm.envDraft);
    expect(vm.activeEnvVars).toEqual([]);
  });

  it("reports Error and string load failures", async () => {
    apiMocks.fetchWorkersPage.mockRejectedValueOnce(new Error("workers unavailable"));
    const wrapper = shallowMount(WorkersPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as { error: string };
    expect(vm.error).toBe("workers unavailable");
    apiMocks.fetchWorkersPage.mockRejectedValueOnce("offline");
    subscriber(domainEvent("Worker"));
    await flushPromises();
    expect(vm.error).toBe("offline");
  });
});
