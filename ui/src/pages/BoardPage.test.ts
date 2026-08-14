import { flushPromises, shallowMount, type VueWrapper } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { BoardData, DomainEventItem, TaskItem, WorkerItem } from "../models";
import PaginationBar from "../components/PaginationBar.vue";
import SearchToolbar from "../components/SearchToolbar.vue";
import TaskDetailModal from "../components/TaskDetailModal.vue";
import BoardPage from "./BoardPage.vue";

const apiMocks = vi.hoisted(() => ({
  subscribeDomainEvents: vi.fn(),
  fetchBoardData: vi.fn(),
  deleteTask: vi.fn(),
  fetchEmployees: vi.fn(),
  createTask: vi.fn(),
}));

vi.mock("../api", () => ({ api: apiMocks }));

const project = {
  id: "project-1",
  name: "Project One",
  gitUrl: "repo",
  defaultBranch: "main",
  worktreeNamePrefix: "task",
  archived: false,
  createdAt: "now",
  updatedAt: "now",
};

const secondProject = { ...project, id: "project-2", name: "Project Two" };

function makeTask(id: string, status: string, startDate = "2026-08-10T00:00:00Z", endDate = "2026-08-12T00:00:00Z"): TaskItem {
  return {
    id,
    title: `Task ${id}`,
    description: `Description ${id}`,
    status,
    projectId: project.id,
    workerId: "worker-all",
    agentType: "codex",
    agentConfig: {},
    baseBranch: "main",
    preCommands: [],
    postCommands: [],
    ownerUserId: "user-1",
    startDate,
    endDate,
    createdAt: "now",
    updatedAt: "now",
  };
}

const workers: WorkerItem[] = [
  {
    id: "worker-all",
    name: "All Projects",
    status: "ONLINE",
    capabilities: [],
    supportedAgents: ["codex", "claude"],
    workDir: "/all",
    projectBindingMode: "ALL_PROJECTS",
    boundProjectIds: [],
    agentRuntimeEnv: [],
    currentTaskIds: [],
    createdAt: "now",
    updatedAt: "now",
  },
  {
    id: "worker-specific",
    name: "Specific Project",
    status: "ONLINE",
    capabilities: [],
    supportedAgents: ["claude"],
    workDir: "/specific",
    projectBindingMode: "SPECIFIC_PROJECTS",
    boundProjectIds: [project.id],
    agentRuntimeEnv: [],
    currentTaskIds: [],
    createdAt: "now",
    updatedAt: "now",
  },
  {
    id: "worker-offline",
    name: "Offline",
    status: "OFFLINE",
    capabilities: [],
    supportedAgents: ["codex"],
    workDir: "/offline",
    projectBindingMode: "ALL_PROJECTS",
    boundProjectIds: [],
    agentRuntimeEnv: [],
    currentTaskIds: [],
    createdAt: "now",
    updatedAt: "now",
  },
  {
    id: "worker-empty",
    name: "No Agents",
    status: "ONLINE",
    capabilities: [],
    supportedAgents: [],
    workDir: "/empty",
    projectBindingMode: "ALL_PROJECTS",
    boundProjectIds: [],
    agentRuntimeEnv: [],
    currentTaskIds: [],
    createdAt: "now",
    updatedAt: "now",
  },
];

const tasks = [
  makeTask("created", "CREATED"),
  makeTask("pending", "PENDING", "2026-08-11", "2026-08-10"),
  makeTask("assigned", "ASSIGNED", "2026-08-13", ""),
  makeTask("starting", "STARTING", "", "2026-08-14"),
  makeTask("running", "RUNNING", "2026-08-10", "2026-08-10"),
  makeTask("waiting", "WAITING"),
  makeTask("waiting-input", "WAITING_INPUT"),
  makeTask("interrupting", "INTERRUPTING"),
  makeTask("completed", "COMPLETED"),
  makeTask("failed", "FAILED", "invalid", "invalid"),
  makeTask("archived", "ARCHIVED"),
];

const boardData: BoardData = {
  id: "board",
  name: "Board",
  type: "KANBAN",
  totalCount: tasks.length,
  columns: [],
  calendarItems: [],
  tasks,
  projects: [project, secondProject],
  workers,
};

type BoardVM = {
  view: string;
  board: BoardData;
  error: string;
  page: { offset: number; limit: number };
  search: string;
  sort: { field: string; direction: "ASC" | "DESC" };
  selectedProjectId: string;
  selectedTaskId: string;
  taskDialogOpen: boolean;
  draftTask: Record<string, unknown>;
  draftStartDate: string;
  draftEndDate: string;
  draftAgentConfig: Record<string, unknown>;
  draftProjectId: string;
  draftWorkerId: string;
  draftAgentType: string;
  availableDraftWorkers: WorkerItem[];
  selectedDraftWorker?: WorkerItem;
  draftAgentOptions: string[];
  canSaveDraftTask: boolean;
  scrumColumns: Array<{ id: string; tasks: TaskItem[] }>;
  calendarMode: "Month" | "Week" | "Day" | "Year";
  draftCalendarFocus: string;
  calendarFocus: Date;
  calendarRanges: Array<{ task: TaskItem; start: Date; end: Date }>;
  calendarMonthDays: Date[];
  calendarWeekDays: Date[];
  calendarYearMonths: Date[];
  periodLabel: string;
  load: (showSpinner?: boolean) => Promise<void>;
  setView: (view: "KANBAN" | "LIST" | "CALENDAR" | "ARCHIVED") => void;
  taskLabel: (task: TaskItem) => string;
  openTask: (task: TaskItem) => void;
  deleteTask: (task: TaskItem) => Promise<void>;
  openNewTask: () => Promise<void>;
  saveTask: () => Promise<void>;
  updateDraftProject: (id: string) => void;
  updateDraftWorker: (id: string) => void;
  reconcileDraftTaskSelection: () => void;
  taskDateInput: (value: string) => string | undefined;
  availableWorkersForProject: (id: string) => WorkerItem[];
  workerAvailableForProject: (worker: WorkerItem, projectId: string) => boolean;
  workerAllowsProject: (worker: WorkerItem, projectId: string) => boolean;
  buildScrumColumns: (tasks: TaskItem[]) => Array<{ id: string; tasks: TaskItem[] }>;
  scrumColumnId: (status: string) => string;
  taskRanges: (tasks: TaskItem[]) => Array<{ task: TaskItem; start: Date; end: Date }>;
  localDate: (value?: string) => Date;
  sameDay: (left: Date, right: Date) => boolean;
  isTaskVisibleOn: (range: { task: TaskItem; start: Date; end: Date }, day: Date) => boolean;
  rangesForDay: (day: Date) => Array<{ task: TaskItem }>;
  rangesForMonth: (day: Date) => Array<{ task: TaskItem }>;
  daysBetween: (start: Date, end: Date) => Date[];
  moveCalendar: (delta: number) => void;
};

describe("BoardPage", () => {
  let subscriber: (event: DomainEventItem) => void;
  let reset: () => void;
  let unsubscribe: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    unsubscribe = vi.fn();
    apiMocks.subscribeDomainEvents.mockImplementation((callback: (event: DomainEventItem) => void, _filter: unknown, onReset: () => void) => {
      subscriber = callback;
      reset = onReset;
      return unsubscribe;
    });
    apiMocks.fetchBoardData.mockResolvedValue(boardData);
    apiMocks.deleteTask.mockResolvedValue(undefined);
    apiMocks.fetchEmployees.mockResolvedValue([{ id: "user-1", name: "User One" }]);
    apiMocks.createTask.mockResolvedValue(tasks[0]);
  });

  async function mountBoard(): Promise<{ wrapper: VueWrapper; vm: BoardVM }> {
    const wrapper = shallowMount(BoardPage, {
      props: { ownerUserId: "owner-1", currentUserId: "user-1" },
      global: {
        stubs: {
          PageHeader: { template: '<header><slot name="actions" /></header>' },
        },
      },
    });
    await flushPromises();
    return { wrapper, vm: wrapper.vm as unknown as BoardVM };
  }

  it("loads, groups and refreshes Board data", async () => {
    const { wrapper, vm } = await mountBoard();
    expect(vm.board).toEqual(boardData);
    expect(vm.scrumColumns.find((column) => column.id === "backlog")?.tasks).toHaveLength(2);
    expect(vm.scrumColumns.find((column) => column.id === "ready")?.tasks).toHaveLength(1);
    expect(vm.scrumColumns.find((column) => column.id === "in-progress")?.tasks).toHaveLength(5);
    expect(vm.scrumColumns.find((column) => column.id === "done")?.tasks).toHaveLength(2);
    expect(vm.taskLabel(tasks[0])).toContain("Project One");

    subscriber({ eventId: "event", eventType: "Updated", aggregateType: "Settings" } as DomainEventItem);
    subscriber({ eventId: "event", eventType: "Updated", aggregateType: "Task" } as DomainEventItem);
    reset();
    await flushPromises();
    expect(apiMocks.fetchBoardData.mock.calls.length).toBeGreaterThanOrEqual(3);

    vm.search = "Task";
    await wrapper.vm.$nextTick();
    vm.page = { offset: 20, limit: 20 };
    await wrapper.vm.$nextTick();
    await flushPromises();
    wrapper.unmount();
    expect(unsubscribe).toHaveBeenCalled();
  });

  it("creates tasks with and without optional assignment fields", async () => {
    const { wrapper, vm } = await mountBoard();
    await vm.openNewTask();
    expect(vm.taskDialogOpen).toBe(true);
    expect(vm.draftTask).toMatchObject({ projectId: project.id, ownerUserId: "user-1" });
    expect(vm.canSaveDraftTask).toBe(false);

    vm.draftTask.title = "Unassigned task";
    vm.draftTask.ownerUserId = "";
    await vm.saveTask();
    expect(apiMocks.createTask).toHaveBeenLastCalledWith(expect.not.objectContaining({ workerId: expect.anything() }));

    await vm.openNewTask();
    vm.draftTask.title = "Assigned task";
    vm.updateDraftWorker("worker-all");
    vm.draftTask.agentType = "codex";
    vm.draftStartDate = "2026-08-20";
    vm.draftEndDate = "2026-08-21";
    vm.draftAgentConfig.workMode = "IMPLEMENT";
    vm.draftAgentConfig.codexModel = "gpt";
    expect(vm.canSaveDraftTask).toBe(true);
    await vm.saveTask();
    expect(apiMocks.createTask).toHaveBeenLastCalledWith(expect.objectContaining({
      workerId: "worker-all",
      agentType: "codex",
      startDate: "2026-08-20T00:00:00Z",
      endDate: "2026-08-21T00:00:00Z",
      agentConfig: expect.objectContaining({ workMode: "IMPLEMENT" }),
    }));

    vm.updateDraftProject(secondProject.id);
    expect(vm.draftWorkerId).toBe("worker-all");
    vm.updateDraftWorker("worker-specific");
    expect(vm.draftAgentType).toBe("");
    vm.draftTask.agentType = "codex";
    vm.reconcileDraftTaskSelection();
    expect(vm.draftAgentType).toBe("");
    vm.updateDraftProject("missing-project");
    expect(vm.draftWorkerId).toBe("");
    expect(vm.taskDateInput("")).toBeUndefined();
    expect(vm.taskDateInput("2026-01-01")).toBe("2026-01-01T00:00:00Z");
    wrapper.unmount();
  });

  it("filters assignable Workers and covers status mappings", async () => {
    const { wrapper, vm } = await mountBoard();
    expect(vm.availableWorkersForProject(project.id).map((item) => item.id)).toEqual(["worker-all", "worker-specific"]);
    expect(vm.availableWorkersForProject(secondProject.id).map((item) => item.id)).toEqual(["worker-all"]);
    expect(vm.workerAvailableForProject(workers[2], project.id)).toBe(false);
    expect(vm.workerAvailableForProject(workers[3], project.id)).toBe(false);
    expect(vm.workerAllowsProject(workers[0], "unknown")).toBe(true);
    expect(vm.workerAllowsProject(workers[1], "unknown")).toBe(false);
    expect(vm.scrumColumnId(" created ")).toBe("backlog");
    expect(vm.scrumColumnId("PENDING")).toBe("backlog");
    expect(vm.scrumColumnId("ASSIGNED")).toBe("ready");
    expect(vm.scrumColumnId("RUNNING")).toBe("in-progress");
    expect(vm.scrumColumnId("FAILED")).toBe("done");
    expect(vm.buildScrumColumns([makeTask("archived-only", "ARCHIVED")]).every((column) => column.tasks.length === 0)).toBe(true);
    wrapper.unmount();
  });

  it("computes date ranges and navigates every calendar mode", async () => {
    const { wrapper, vm } = await mountBoard();
    expect(vm.calendarRanges.some((range) => range.task.id === "archived")).toBe(false);
    const reversed = vm.calendarRanges.find((range) => range.task.id === "pending")!;
    expect(reversed.end.getTime()).toBe(reversed.start.getTime());
    expect(vm.localDate("2026-08-10").getDate()).toBe(10);
    expect(vm.localDate("invalid")).toBeInstanceOf(Date);
    expect(vm.sameDay(new Date(2026, 7, 10), new Date(2026, 7, 10))).toBe(true);
    expect(vm.sameDay(new Date(2026, 7, 10), new Date(2026, 8, 10))).toBe(false);
    expect(vm.isTaskVisibleOn(vm.calendarRanges[0], vm.calendarRanges[0].start)).toBe(true);
    expect(vm.isTaskVisibleOn(vm.calendarRanges[0], new Date(2030, 1, 1))).toBe(false);
    expect(vm.rangesForDay(vm.calendarRanges[0].start).length).toBeGreaterThan(0);
    expect(vm.rangesForMonth(vm.calendarRanges[0].start).length).toBeGreaterThan(0);
    expect(vm.rangesForMonth(new Date(2030, 1, 1))).toEqual([]);
    expect(vm.daysBetween(new Date(2026, 7, 10), new Date(2026, 7, 12))).toHaveLength(3);
    expect(vm.calendarMonthDays.length).toBeGreaterThanOrEqual(35);
    expect(vm.calendarWeekDays).toHaveLength(7);
    expect(vm.calendarYearMonths).toHaveLength(12);

    vm.draftCalendarFocus = "2026-08-10T00:00:00Z";
    for (const mode of ["Day", "Week", "Month", "Year"] as const) {
      vm.calendarMode = mode;
      await wrapper.vm.$nextTick();
      expect(vm.periodLabel.length).toBeGreaterThan(0);
      vm.moveCalendar(1);
      expect(vm.draftCalendarFocus).not.toBe("");
    }
    wrapper.unmount();
  });

  it("renders all Board views and executes template event handlers", async () => {
    const { wrapper, vm } = await mountBoard();
    const topButton = (text: string) => wrapper.findAllComponents({ name: "AButton" }).find((button) => button.text() === text)!;
    for (const [label, expected] of [["Kanban", "KANBAN"], ["List", "LIST"], ["Archived", "ARCHIVED"], ["Calendar", "CALENDAR"]] as const) {
      topButton(label).vm.$emit("click");
      await wrapper.vm.$nextTick();
      await flushPromises();
      expect(vm.view).toBe(expected);
    }

    for (const mode of ["Month", "Week", "Day", "Year"] as const) {
      const button = wrapper.findAllComponents({ name: "AButton" }).find((item) => item.text() === mode);
      button?.vm.$emit("click");
      await wrapper.vm.$nextTick();
      expect(vm.calendarMode).toBe(mode);
    }
    vm.setView("KANBAN");
    await wrapper.vm.$nextTick();
    await wrapper.find(".task-card").trigger("click");
    expect(vm.selectedTaskId).not.toBe("");
    await wrapper.vm.$nextTick();
    wrapper.findComponent(TaskDetailModal).vm.$emit("changed");
    wrapper.findComponent(TaskDetailModal).vm.$emit("close");
    await flushPromises();
    expect(vm.selectedTaskId).toBe("");

    vm.setView("ARCHIVED");
    await wrapper.vm.$nextTick();
    const deleteButton = wrapper.findAllComponents({ name: "AButton" }).find((button) => button.attributes("aria-label") === "Delete task")!;
    deleteButton.vm.$emit("click");
    await flushPromises();
    expect(apiMocks.deleteTask).toHaveBeenCalled();

    wrapper.findComponent(PaginationBar).vm.$emit("change", { offset: 20, limit: 20 });
    wrapper.findComponent(SearchToolbar).vm.$emit("update:search", "query");
    wrapper.findComponent(SearchToolbar).vm.$emit("update:sort", { field: "TITLE", direction: "ASC" });
    wrapper.findComponent(SearchToolbar).vm.$emit("update:project", project.id);
    await wrapper.vm.$nextTick();
    expect(vm.page.offset).toBe(0);
    expect(vm.search).toBe("query");
    expect(vm.selectedProjectId).toBe(project.id);
    wrapper.unmount();
  });

  it("reports Error and string load failures", async () => {
    apiMocks.fetchBoardData.mockRejectedValueOnce(new Error("board unavailable"));
    const wrapper = shallowMount(BoardPage);
    await flushPromises();
    const vm = wrapper.vm as unknown as BoardVM;
    expect(vm.error).toBe("board unavailable");
    apiMocks.fetchBoardData.mockRejectedValueOnce("offline");
    reset();
    await flushPromises();
    expect(vm.error).toBe("offline");
    wrapper.unmount();
  });
});
