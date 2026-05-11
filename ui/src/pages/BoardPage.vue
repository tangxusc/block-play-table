<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { PlusOutlined, ReloadOutlined } from "@ant-design/icons-vue";
import { api } from "../api";
import type { BoardData, BoardView, PageRequest, SortRequest, TaskItem, WorkerItem } from "../models";
import { defaultPage, defaultSort } from "../models";
import { agentConfigInput, emptyAgentConfigDraft, type AgentConfigDraft } from "../agentConfig";
import { addDays, dateOnly, formatDay, formatMonth, parseDate, projectName, startOfWeek, taskDateRange } from "../format";
import PageHeader from "../components/PageHeader.vue";
import SearchToolbar from "../components/SearchToolbar.vue";
import PaginationBar from "../components/PaginationBar.vue";
import TaskDetailModal from "../components/TaskDetailModal.vue";
import AgentConfigFields from "../components/AgentConfigFields.vue";

interface CalendarTaskRange {
  task: TaskItem;
  start: Date;
  end: Date;
}

interface ScrumColumn {
  id: string;
  title: string;
  status: string;
  tasks: TaskItem[];
}

const view = ref<BoardView>("KANBAN");
const page = ref<PageRequest>(defaultPage());
const sort = ref<SortRequest>(defaultSort());
const search = ref("");
const selectedProjectId = ref("");
const loading = ref(false);
const error = ref("");
const board = ref<BoardData>(emptyBoard());
const selectedTaskId = ref("");
const taskDialogOpen = ref(false);
const draftTask = ref<Record<string, unknown>>({});
const draftAgentConfig = ref<AgentConfigDraft>(emptyAgentConfigDraft());
const calendarMode = ref<"Month" | "Week" | "Day" | "Year">("Month");
const draftCalendarFocus = ref("");
let unsubscribe: (() => void) | undefined;
let pollTimer: number | undefined;

const activeTasks = computed(() => board.value.tasks);
const scrumColumns = computed<ScrumColumn[]>(() => buildScrumColumns(activeTasks.value));
const draftProjectId = computed(() => String(draftTask.value.projectId || ""));
const draftWorkerId = computed(() => String(draftTask.value.workerId || ""));
const draftAgentType = computed(() => String(draftTask.value.agentType || ""));
const availableDraftWorkers = computed(() => availableWorkersForProject(draftProjectId.value));
const selectedDraftWorker = computed(() =>
  availableDraftWorkers.value.find((worker) => worker.id === draftWorkerId.value),
);
const draftAgentOptions = computed(() => selectedDraftWorker.value?.supportedAgents || []);
const canSaveDraftTask = computed(
  () =>
    String(draftTask.value.title || "").trim().length > 0 &&
    draftProjectId.value.length > 0 &&
    (!draftWorkerId.value || draftAgentType.value.length > 0),
);
const calendarRanges = computed(() => taskRanges(activeTasks.value));
const calendarFocus = computed(() =>
  draftCalendarFocus.value
    ? parseDate(draftCalendarFocus.value)
    : calendarRanges.value[0]?.start || parseDate(activeTasks.value[0]?.startDate),
);
const calendarTitle = computed(() => {
  const focus = calendarFocus.value;
  if (calendarMode.value === "Month") return formatMonth(focus);
  if (calendarMode.value === "Day") return formatDay(focus);
  if (calendarMode.value === "Year") return String(focus.getFullYear());
  const start = startOfWeek(focus);
  return `${formatDay(start)} - ${formatDay(addDays(start, 6))}`;
});
const calendarMonthDays = computed(() => {
  const focus = calendarFocus.value;
  const first = new Date(focus.getFullYear(), focus.getMonth(), 1);
  const last = new Date(focus.getFullYear(), focus.getMonth() + 1, 0);
  const gridStart = startOfWeek(first);
  const gridEnd = addDays(startOfWeek(last), 6);
  return daysBetween(gridStart, gridEnd);
});
const calendarWeekDays = computed(() => {
  const start = startOfWeek(calendarFocus.value);
  return daysBetween(start, addDays(start, 6));
});
const calendarYearMonths = computed(() => {
  const year = calendarFocus.value.getFullYear();
  return Array.from({ length: 12 }, (_, index) => new Date(year, index, 1));
});

watch([view, search, sort, selectedProjectId], () => {
  page.value = defaultPage();
  void load();
});

watch(page, () => void load());

void load();
unsubscribe = api.subscribeDomainEvents(
  (event) => {
    if (["Task", "Worker", "Project"].includes(event.aggregateType)) void load(false);
  },
  {},
  () => void load(false),
);
pollTimer = window.setInterval(() => void load(false), 15000);

onBeforeUnmount(() => {
  unsubscribe?.();
  window.clearInterval(pollTimer);
});

async function load(showSpinner = true) {
  if (showSpinner) loading.value = true;
  error.value = "";
  try {
    board.value = await api.fetchBoardData(
      view.value,
      page.value,
      search.value,
      sort.value,
      selectedProjectId.value,
    );
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause);
  } finally {
    loading.value = false;
  }
}

function setView(next: BoardView) {
  view.value = next;
  calendarMode.value = "Month";
  draftCalendarFocus.value = "";
}

function taskLabel(task: TaskItem) {
  return `${task.title} ${task.status} ${projectName(board.value.projects, task.projectId)}`;
}

function openTask(task: TaskItem) {
  selectedTaskId.value = task.id;
}

async function deleteTask(task: TaskItem) {
  await api.deleteTask(task.id);
  await load(false);
}

function openNewTask() {
  draftTask.value = {
    title: "",
    description: "",
    projectId: board.value.projects[0]?.id || "",
    workerId: "",
    agentType: "",
    baseBranch: "main",
  };
  draftAgentConfig.value = emptyAgentConfigDraft();
  reconcileDraftTaskSelection();
  taskDialogOpen.value = true;
}

async function saveTask() {
  const input = { ...draftTask.value };
  if (!input.workerId) delete input.workerId;
  if (!input.agentType) delete input.agentType;
  if (draftAgentType.value) {
    input.agentConfig = agentConfigInput(draftAgentType.value, draftAgentConfig.value);
  }
  await api.createTask(input);
  taskDialogOpen.value = false;
  await load(false);
}

function updateDraftProject(projectId: string) {
  draftTask.value.projectId = projectId;
  reconcileDraftTaskSelection();
}

function updateDraftWorker(workerId: string) {
  draftTask.value.workerId = workerId;
  draftTask.value.agentType = "";
  reconcileDraftTaskSelection();
}

function reconcileDraftTaskSelection() {
  if (draftWorkerId.value && !availableDraftWorkers.value.some((worker) => worker.id === draftWorkerId.value)) {
    draftTask.value.workerId = "";
    draftTask.value.agentType = "";
  }
  if (!draftTask.value.workerId) {
    draftTask.value.agentType = "";
    return;
  }
  const worker = selectedDraftWorker.value;
  if (!worker) return;
  const currentAgent = String(draftTask.value.agentType || "");
  if (!worker.supportedAgents.includes(currentAgent)) {
    draftTask.value.agentType = "";
  }
}

function availableWorkersForProject(projectId: string): WorkerItem[] {
  return board.value.workers.filter((worker) => workerAvailableForProject(worker, projectId));
}

function workerAvailableForProject(worker: WorkerItem, projectId: string): boolean {
  return worker.status === "ONLINE" && worker.supportedAgents.length > 0 && workerAllowsProject(worker, projectId);
}

function workerAllowsProject(worker: WorkerItem, projectId: string): boolean {
  return worker.projectBindingMode === "ALL_PROJECTS" || worker.boundProjectIds.includes(projectId);
}

function emptyBoard(): BoardData {
  return {
    id: "default",
    name: "Default Board",
    type: "KANBAN",
    totalCount: 0,
    columns: [],
    calendarItems: [],
    tasks: [],
    projects: [],
    workers: [],
  };
}

function buildScrumColumns(tasks: TaskItem[]): ScrumColumn[] {
  const columns: ScrumColumn[] = [
    { id: "backlog", title: "Backlog", status: "CREATED", tasks: [] },
    { id: "ready", title: "Ready", status: "ASSIGNED", tasks: [] },
    { id: "in-progress", title: "In Progress", status: "RUNNING", tasks: [] },
    { id: "done", title: "Done", status: "COMPLETED", tasks: [] },
  ];
  const byId = Object.fromEntries(columns.map((column) => [column.id, column]));
  for (const task of tasks) {
    if (task.status === "ARCHIVED") continue;
    byId[scrumColumnId(task.status)].tasks.push(task);
  }
  return columns;
}

function scrumColumnId(status: string): string {
  const normalized = status.trim().toUpperCase();
  if (["CREATED", "PENDING"].includes(normalized)) return "backlog";
  if (normalized === "ASSIGNED") return "ready";
  if (["STARTING", "RUNNING", "WAITING", "WAITING_INPUT", "INTERRUPTING"].includes(normalized)) {
    return "in-progress";
  }
  return "done";
}

function taskRanges(tasks: TaskItem[]): CalendarTaskRange[] {
  return tasks
    .filter((task) => task.status !== "ARCHIVED" && (task.startDate || task.endDate))
    .map((task) => {
      const start = localDate(task.startDate || task.endDate);
      const rawEnd = localDate(task.endDate || task.startDate);
      return { task, start, end: rawEnd < start ? start : rawEnd };
    })
    .sort((a, b) => a.start.getTime() - b.start.getTime() || a.task.title.localeCompare(b.task.title));
}

function localDate(value?: string): Date {
  const date = dateOnly(value);
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(date);
  if (!match) {
    const fallback = parseDate(value);
    return new Date(fallback.getFullYear(), fallback.getMonth(), fallback.getDate());
  }
  return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
}

function sameDay(left: Date, right: Date): boolean {
  return (
    left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
  );
}

function isTaskVisibleOn(range: CalendarTaskRange, day: Date): boolean {
  const current = new Date(day.getFullYear(), day.getMonth(), day.getDate()).getTime();
  return current >= range.start.getTime() && current <= range.end.getTime();
}

function rangesForDay(day: Date): CalendarTaskRange[] {
  return calendarRanges.value.filter((range) => isTaskVisibleOn(range, day));
}

function rangesForMonth(month: Date): CalendarTaskRange[] {
  const first = new Date(month.getFullYear(), month.getMonth(), 1);
  const last = new Date(month.getFullYear(), month.getMonth() + 1, 0);
  return calendarRanges.value.filter((range) => range.end >= first && range.start <= last);
}

function daysBetween(start: Date, end: Date): Date[] {
  const days: Date[] = [];
  for (let day = start; day <= end; day = addDays(day, 1)) {
    days.push(day);
  }
  return days;
}

function moveCalendar(delta: number) {
  const focus = calendarFocus.value;
  const next =
    calendarMode.value === "Day"
      ? addDays(focus, delta)
      : calendarMode.value === "Week"
        ? addDays(focus, delta * 7)
        : calendarMode.value === "Year"
          ? new Date(focus.getFullYear() + delta, focus.getMonth(), 1)
          : new Date(focus.getFullYear(), focus.getMonth() + delta, 1);
  draftCalendarFocus.value = next.toISOString();
}
</script>

<template>
  <div>
    <PageHeader title="Board">
      <template #actions>
        <a-space>
          <a-button
            v-for="item in ['KANBAN', 'LIST', 'CALENDAR', 'ARCHIVED']"
            :key="item"
            :type="view === item ? 'primary' : 'default'"
            @click="setView(item as BoardView)"
          >
            {{ item === "KANBAN" ? "Kanban" : item === "LIST" ? "List" : item === "CALENDAR" ? "Calendar" : "Archived" }}
          </a-button>
          <a-button aria-label="Refresh board" @click="load()"><ReloadOutlined /> Refresh</a-button>
          <a-button type="primary" @click="openNewTask"><PlusOutlined /> New task</a-button>
        </a-space>
      </template>
    </PageHeader>

    <main class="content">
      <SearchToolbar
        :search="search"
        :sort="sort"
        :projects="board.projects"
        :selected-project-id="selectedProjectId"
        :sort-options="[
          { field: 'CREATED_AT', label: 'Created' },
          { field: 'UPDATED_AT', label: 'Updated' },
          { field: 'TITLE', label: 'Title' },
          { field: 'STATUS', label: 'Status' },
          { field: 'START_DATE', label: 'Start date' },
          { field: 'END_DATE', label: 'End date' },
        ]"
        @update:search="search = $event"
        @update:sort="sort = $event"
        @update:project="selectedProjectId = $event"
      />

      <a-alert v-if="error" type="error" show-icon :message="error" style="margin-bottom: 12px" />
      <a-spin :spinning="loading">
        <section v-if="view === 'KANBAN'" class="board-grid">
          <div
            v-for="column in scrumColumns"
            :key="column.id"
            class="board-column"
          >
            <div class="board-column-header">
              <span>{{ column.title }}</span>
              <a-tag>{{ column.tasks.length }}</a-tag>
            </div>
            <div class="task-list">
              <div
                v-for="task in column.tasks"
                :key="task.id"
                role="group"
                :aria-label="taskLabel(task)"
              >
                <button class="task-card" :aria-label="`Open task ${task.id}`" @click="openTask(task)">
                  <div class="task-title">{{ task.title }}</div>
                  <div class="task-meta">
                    <a-tag>{{ task.status }}</a-tag>
                    <span>{{ projectName(board.projects, task.projectId) }}</span>
                    <span>{{ taskDateRange(task) }}</span>
                  </div>
                </button>
              </div>
            </div>
          </div>
        </section>

        <section v-else-if="view === 'LIST' || view === 'ARCHIVED'" class="record-list">
          <div
            v-for="task in board.tasks"
            :key="task.id"
            class="record-card"
            role="group"
            :aria-label="taskLabel(task)"
          >
            <div class="record-header">
              <button class="task-card" style="border: 0; padding: 0" @click="openTask(task)">
                <div class="task-title">{{ task.title }}</div>
                <div class="record-subtitle">
                  <span>{{ projectName(board.projects, task.projectId) }} - {{ task.status }}</span>
                  <span v-if="false">
                  {{ projectName(board.projects, task.projectId) }} · {{ task.status }}
                  </span>
                </div>
              </button>
              <a-space>
                <a-button v-if="view === 'ARCHIVED'" aria-label="Delete task" danger @click="deleteTask(task)">
                  Delete task
                </a-button>
              </a-space>
            </div>
          </div>
        </section>

        <section v-else class="calendar-panel">
          <div class="calendar-toolbar">
            <div class="calendar-title">{{ calendarTitle }}</div>
            <a-space>
              <a-button
                v-for="mode in ['Month', 'Week', 'Day', 'Year']"
                :key="mode"
                :type="calendarMode === mode ? 'primary' : 'default'"
                @click="calendarMode = mode as typeof calendarMode"
              >
                {{ mode }}
              </a-button>
              <a-button aria-label="Previous period" @click="moveCalendar(-1)">Previous</a-button>
              <a-button @click="draftCalendarFocus = new Date().toISOString()">Today</a-button>
              <a-button aria-label="Next period" @click="moveCalendar(1)">Next</a-button>
            </a-space>
          </div>
          <template v-if="calendarRanges.length === 0">
            <a-empty description="No scheduled tasks" />
          </template>

          <template v-else-if="calendarMode === 'Month'">
            <div class="calendar-weekdays">
              <span v-for="day in ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']" :key="day">{{ day }}</span>
            </div>
            <div class="calendar-grid calendar-month-grid">
              <div
                v-for="day in calendarMonthDays"
                :key="day.toISOString()"
                class="calendar-cell"
                :class="{ 'calendar-cell-muted': day.getMonth() !== calendarFocus.getMonth() }"
              >
                <div class="calendar-day-number">{{ day.getDate() }}</div>
                <button
                  v-for="range in rangesForDay(day)"
                  :key="range.task.id"
                  class="calendar-task"
                  :aria-label="`${range.task.title} ${dateOnly(range.task.startDate)} ${dateOnly(range.task.endDate)}`"
                  @click="openTask(range.task)"
                >
                  {{ range.task.title }}
                </button>
              </div>
            </div>
          </template>

          <template v-else-if="calendarMode === 'Week'">
            <div class="calendar-grid calendar-week-grid">
              <div
                v-for="day in calendarWeekDays"
                :key="day.toISOString()"
                class="calendar-cell"
              >
                <div class="calendar-day-number">{{ formatDay(day) }}</div>
                <button
                  v-for="range in rangesForDay(day)"
                  :key="range.task.id"
                  class="calendar-task"
                  :aria-label="`${range.task.title} ${dateOnly(range.task.startDate)} ${dateOnly(range.task.endDate)}`"
                  @click="openTask(range.task)"
                >
                  {{ range.task.title }}
                </button>
              </div>
            </div>
          </template>

          <template v-else-if="calendarMode === 'Day'">
            <div class="calendar-day-view">
              <div class="calendar-day-number">{{ formatDay(calendarFocus) }}</div>
              <div class="record-list">
                <div v-for="range in rangesForDay(calendarFocus)" :key="range.task.id" class="record-card">
                  <button class="task-card" @click="openTask(range.task)">
                    {{ range.task.title }} - {{ dateOnly(range.task.startDate) }} - {{ dateOnly(range.task.endDate) }}
                  </button>
                </div>
              </div>
            </div>
          </template>

          <template v-else>
            <div class="calendar-year-grid">
              <section v-for="month in calendarYearMonths" :key="month.toISOString()" class="calendar-month-card">
                <button class="calendar-month-title" @click="calendarMode = 'Month'; draftCalendarFocus = month.toISOString()">
                  {{ formatMonth(month) }}
                </button>
                <button
                  v-for="range in rangesForMonth(month).slice(0, 4)"
                  :key="range.task.id"
                  class="calendar-task"
                  @click="openTask(range.task)"
                >
                  {{ range.task.title }}
                </button>
              </section>
            </div>
          </template>

          <div v-if="false" class="record-list">
            <div v-for="task in activeTasks" :key="task.id" class="record-card">
              <button class="task-card" @click="openTask(task)">
                {{ task.title }} · {{ dateOnly(task.startDate) }} - {{ dateOnly(task.endDate) }}
              </button>
            </div>
          </div>
        </section>

        <PaginationBar
          :page="page"
          :total="board.totalCount"
          @change="page = $event"
        />
      </a-spin>
    </main>

    <TaskDetailModal
      v-if="selectedTaskId"
      :task-id="selectedTaskId"
      :board="board"
      @close="selectedTaskId = ''"
      @changed="load(false)"
    />

    <a-modal
      v-model:open="taskDialogOpen"
      title="Create task"
      ok-text="Save"
      :ok-button-props="{ disabled: !canSaveDraftTask }"
      @ok="saveTask"
    >
      <a-form layout="vertical">
        <a-form-item label="Title">
          <a-input v-model:value="draftTask.title" aria-label="Title" />
        </a-form-item>
        <a-form-item label="Description">
          <a-textarea v-model:value="draftTask.description" aria-label="Description" />
        </a-form-item>
        <a-form-item label="Project">
          <a-select :value="draftTask.projectId" aria-label="Project" @change="updateDraftProject">
            <a-select-option v-for="project in board.projects" :key="project.id" :value="project.id">
              {{ project.name }}
            </a-select-option>
          </a-select>
        </a-form-item>
        <a-form-item label="Worker">
          <a-select :value="draftTask.workerId" aria-label="Worker" @change="updateDraftWorker">
            <a-select-option value="">Unassigned</a-select-option>
            <a-select-option v-for="worker in availableDraftWorkers" :key="worker.id" :value="worker.id">
              {{ worker.name }}
            </a-select-option>
          </a-select>
        </a-form-item>
        <a-form-item v-if="selectedDraftWorker" label="Agent">
          <a-select v-model:value="draftTask.agentType" aria-label="Agent">
            <a-select-option v-for="agent in draftAgentOptions" :key="agent" :value="agent">
              {{ agent === "codex" ? "Codex" : agent === "claude" ? "Claude" : agent }}
            </a-select-option>
          </a-select>
        </a-form-item>
        <AgentConfigFields
          v-if="selectedDraftWorker && draftAgentType"
          :agent-type="draftAgentType"
          :draft="draftAgentConfig"
        />
      </a-form>
    </a-modal>
  </div>
</template>
