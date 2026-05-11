import type { PageRequest, ProjectItem, TaskInteraction, TaskItem, WorkerItem } from "./models";

export function pageSummary(page: PageRequest, total: number): string {
  if (total <= 0) return "Showing 0 of 0";
  const start = Math.min(page.offset + 1, total);
  const end = Math.min(page.offset + page.limit, total);
  return `Showing ${start}-${end} of ${total}`;
}

export function lastOffset(total: number, limit: number): number {
  if (total <= 0) return 0;
  return Math.floor((total - 1) / limit) * limit;
}

export function projectName(projects: ProjectItem[], id: string): string {
  return projects.find((project) => project.id === id)?.name || id;
}

export function workerName(workers: WorkerItem[], id?: string): string {
  if (!id) return "Unassigned";
  return workers.find((worker) => worker.id === id)?.name || id;
}

export function taskDateRange(task: TaskItem): string {
  const start = dateOnly(task.startDate);
  const end = dateOnly(task.endDate || task.startDate);
  return start === end ? start : `${start} - ${end}`;
}

export function dateOnly(value?: string): string {
  if (!value) return "";
  return value.slice(0, 10);
}

export function formatMonth(value: Date): string {
  return new Intl.DateTimeFormat("en-US", { month: "long", year: "numeric" }).format(value);
}

export function formatDay(value: Date): string {
  return new Intl.DateTimeFormat("en-US", {
    month: "short",
    day: "numeric",
    year: "numeric",
  }).format(value);
}

export function parseDate(value?: string): Date {
  if (!value) return new Date();
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? new Date() : date;
}

export function startOfWeek(date: Date): Date {
  const out = new Date(date);
  out.setHours(0, 0, 0, 0);
  const day = out.getDay();
  const delta = day === 0 ? -6 : 1 - day;
  out.setDate(out.getDate() + delta);
  return out;
}

export function addDays(date: Date, days: number): Date {
  const out = new Date(date);
  out.setDate(out.getDate() + days);
  return out;
}

export function interactionPayloadSummary(interaction: TaskInteraction): string {
  try {
    const payload = JSON.parse(interaction.rawPayload || "{}");
    const lines: string[] = [];
    if (payload.command) lines.push(String(payload.command));
    if (payload.tool_name) lines.push(`Tool: ${payload.tool_name}`);
    if (payload.tool_input?.file_path) lines.push(String(payload.tool_input.file_path));
    if (payload.cwd) lines.push(String(payload.cwd));
    if (payload.reason) lines.push(String(payload.reason));
    return lines.length ? lines.join("\n") : JSON.stringify(payload, null, 2);
  } catch {
    return interaction.rawPayload || "";
  }
}
