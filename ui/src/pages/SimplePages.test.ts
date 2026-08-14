import { flushPromises, shallowMount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { DomainEventItem, ProjectItem } from "../models";
import PaginationBar from "../components/PaginationBar.vue";
import SearchToolbar from "../components/SearchToolbar.vue";
import EventsPage from "./EventsPage.vue";
import ProjectsPage from "./ProjectsPage.vue";
import SettingsPage from "./SettingsPage.vue";

const apiMocks = vi.hoisted(() => ({
  subscribeDomainEvents: vi.fn(),
  fetchProjectsPage: vi.fn(),
  createProject: vi.fn(),
  updateProject: vi.fn(),
  archiveProject: vi.fn(),
  fetchEventsPage: vi.fn(),
  fetchSettings: vi.fn(),
  fetchCurrentUser: vi.fn(),
  fetchEmployeeByID: vi.fn(),
  updateSettings: vi.fn(),
}));

vi.mock("../api", () => ({ api: apiMocks }));

const project: ProjectItem = {
  id: "project-1",
  name: "Project One",
  gitUrl: "https://example.test/repo.git",
  defaultBranch: "main",
  worktreeNamePrefix: "task",
  archived: false,
  createdAt: "2026-08-10T00:00:00Z",
  updatedAt: "2026-08-10T00:00:00Z",
};

const event: DomainEventItem = {
  eventId: "event-1",
  eventType: "TaskCreated",
  aggregateType: "Task",
  aggregateId: "task-1",
  aggregateVersion: 1,
  payload: "{}",
  occurredAt: "2026-08-10T00:00:00Z",
};

const pageMountOptions = {
  global: {
    stubs: {
      PageHeader: { template: '<header><slot name="actions" /></header>' },
    },
  },
};

describe("ProjectsPage", () => {
  let subscriber: (event: DomainEventItem) => void;
  let unsubscribe: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    unsubscribe = vi.fn();
    apiMocks.subscribeDomainEvents.mockImplementation((callback: (value: DomainEventItem) => void) => {
      subscriber = callback;
      return unsubscribe;
    });
    apiMocks.fetchProjectsPage.mockResolvedValue({ items: [project], totalCount: 1 });
    apiMocks.createProject.mockResolvedValue(project);
    apiMocks.updateProject.mockResolvedValue(project);
    apiMocks.archiveProject.mockResolvedValue(undefined);
  });

  it("loads, refreshes from events and saves new and existing projects", async () => {
    const wrapper = shallowMount(ProjectsPage, pageMountOptions);
    await flushPromises();
    const vm = wrapper.vm as unknown as {
      data: { items: ProjectItem[] };
      draft: ProjectItem | null;
      dialogOpen: boolean;
      search: string;
      openProject: (project?: ProjectItem) => void;
      saveProject: () => Promise<void>;
    };
    expect(vm.data.items[0].name).toBe("Project One");
    const buttons = wrapper.findAllComponents({ name: "AButton" });
    buttons.find((button) => button.attributes("aria-label") === "Refresh projects")?.vm.$emit("click");
    buttons.find((button) => button.text().includes("New project"))?.vm.$emit("click");
    buttons.find((button) => button.attributes("aria-label") === "Edit project")?.vm.$emit("click");
    buttons.find((button) => button.attributes("aria-label") === "Archive project")?.vm.$emit("click");
    wrapper.findComponent(SearchToolbar).vm.$emit("update:search", "from toolbar");
    wrapper.findComponent(SearchToolbar).vm.$emit("update:sort", { field: "NAME", direction: "ASC" });
    wrapper.findComponent(PaginationBar).vm.$emit("change", { offset: 20, limit: 20 });
    wrapper.findComponent({ name: "AModal" }).vm.$emit("update:open", true);
    await wrapper.vm.$nextTick();
    wrapper.findAllComponents({ name: "AInput" }).forEach((input, index) => input.vm.$emit("update:value", `value-${index}`));
    await flushPromises();
    vm.openProject();
    expect(vm.draft?.defaultBranch).toBe("main");
    if (vm.draft) vm.draft.name = "Created";
    await vm.saveProject();
    expect(apiMocks.createProject).toHaveBeenCalled();

    vm.openProject(project);
    if (vm.draft) vm.draft.name = "Updated";
    await vm.saveProject();
    expect(apiMocks.updateProject).toHaveBeenCalled();
    subscriber({ ...event, aggregateType: "Worker" });
    subscriber({ ...event, aggregateType: "Project" });
    await flushPromises();
    expect(apiMocks.fetchProjectsPage.mock.calls.length).toBeGreaterThanOrEqual(4);

    vm.search = "query";
    await wrapper.vm.$nextTick();
    await flushPromises();
    wrapper.unmount();
    expect(unsubscribe).toHaveBeenCalled();
  });

  it("handles missing drafts and load failures", async () => {
    apiMocks.fetchProjectsPage.mockRejectedValueOnce(new Error("projects unavailable"));
    const wrapper = shallowMount(ProjectsPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as { error: string; saveProject: () => Promise<void> };
    expect(vm.error).toBe("projects unavailable");
    await expect(vm.saveProject()).resolves.toBeUndefined();

    apiMocks.fetchProjectsPage.mockRejectedValueOnce("offline");
    subscriber({ ...event, aggregateType: "Project" });
    await flushPromises();
    expect(vm.error).toBe("offline");
  });
});

describe("EventsPage", () => {
  let subscriber: () => void;
  let unsubscribe: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    unsubscribe = vi.fn();
    apiMocks.subscribeDomainEvents.mockImplementation((callback: () => void) => {
      subscriber = callback;
      return unsubscribe;
    });
    apiMocks.fetchEventsPage.mockResolvedValue({ items: [event], totalCount: 1 });
  });

  it("loads events, reacts to filters and unsubscribes", async () => {
    const wrapper = shallowMount(EventsPage, pageMountOptions);
    await flushPromises();
    const vm = wrapper.vm as unknown as {
      data: { items: DomainEventItem[] };
      search: string;
      page: { offset: number; limit: number };
    };
    expect(vm.data.items).toEqual([event]);
    wrapper.findAllComponents({ name: "AButton" }).find((button) => button.attributes("aria-label") === "Refresh events")?.vm.$emit("click");
    wrapper.findComponent(SearchToolbar).vm.$emit("update:search", "from toolbar");
    wrapper.findComponent(SearchToolbar).vm.$emit("update:sort", { field: "EVENT_TYPE", direction: "ASC" });
    wrapper.findComponent(PaginationBar).vm.$emit("change", { offset: 20, limit: 20 });
    subscriber();
    vm.search = "Task";
    await wrapper.vm.$nextTick();
    vm.page = { offset: 20, limit: 20 };
    await wrapper.vm.$nextTick();
    await flushPromises();
    expect(apiMocks.fetchEventsPage.mock.calls.length).toBeGreaterThanOrEqual(3);
    wrapper.unmount();
    expect(unsubscribe).toHaveBeenCalled();
  });

  it("reports Error and string failures", async () => {
    apiMocks.fetchEventsPage.mockRejectedValueOnce(new Error("events unavailable"));
    const wrapper = shallowMount(EventsPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as { error: string };
    expect(vm.error).toBe("events unavailable");
    apiMocks.fetchEventsPage.mockRejectedValueOnce("offline");
    subscriber();
    await flushPromises();
    expect(vm.error).toBe("offline");
  });
});

describe("SettingsPage", () => {
  let subscriber: (event: DomainEventItem) => void;
  let unsubscribe: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    unsubscribe = vi.fn();
    apiMocks.subscribeDomainEvents.mockImplementation((callback: (value: DomainEventItem) => void) => {
      subscriber = callback;
      return unsubscribe;
    });
    apiMocks.fetchSettings.mockResolvedValue({ workerHeartbeatTimeout: "30s", securityPolicy: "trusted" });
    apiMocks.fetchCurrentUser.mockResolvedValue({ id: "user-1", trustMode: true });
    apiMocks.fetchEmployeeByID.mockResolvedValue({ id: "user-1", name: "User One" });
    apiMocks.updateSettings.mockResolvedValue(undefined);
  });

  it("loads identity, saves settings and follows Settings events", async () => {
    const wrapper = shallowMount(SettingsPage, pageMountOptions);
    await flushPromises();
    const vm = wrapper.vm as unknown as {
      employeeName: string | null;
      settings: { workerHeartbeatTimeout: string };
      save: () => Promise<void>;
    };
    expect(vm.employeeName).toBe("User One");
    const buttons = wrapper.findAllComponents({ name: "AButton" });
    buttons.find((button) => button.attributes("aria-label") === "Refresh settings")?.vm.$emit("click");
    buttons.find((button) => button.text().includes("Save settings"))?.vm.$emit("click");
    wrapper.findComponent({ name: "AInput" }).vm.$emit("update:value", "60s");
    await flushPromises();
    vm.settings.workerHeartbeatTimeout = "45s";
    await vm.save();
    expect(apiMocks.updateSettings).toHaveBeenCalledWith(expect.objectContaining({ workerHeartbeatTimeout: "45s" }));
    subscriber({ ...event, aggregateType: "Worker" });
    subscriber({ ...event, aggregateType: "Settings" });
    await flushPromises();
    wrapper.unmount();
    expect(unsubscribe).toHaveBeenCalled();
  });

  it("uses unavailable identity and reports both failure shapes", async () => {
    apiMocks.fetchEmployeeByID.mockResolvedValueOnce(null);
    const wrapper = shallowMount(SettingsPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as { employeeName: string | null; error: string };
    expect(vm.employeeName).toBeNull();

    apiMocks.fetchSettings.mockRejectedValueOnce(new Error("settings unavailable"));
    subscriber({ ...event, aggregateType: "Settings" });
    await flushPromises();
    expect(vm.error).toBe("settings unavailable");
    apiMocks.fetchSettings.mockRejectedValueOnce("offline");
    subscriber({ ...event, aggregateType: "Settings" });
    await flushPromises();
    expect(vm.error).toBe("offline");
  });
});
