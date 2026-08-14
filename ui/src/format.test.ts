import { describe, expect, it, vi } from "vitest";
import {
  addDays,
  dateOnly,
  formatDay,
  formatMonth,
  interactionPayloadSummary,
  lastOffset,
  pageSummary,
  parseDate,
  projectName,
  startOfWeek,
  taskDateRange,
  workerName,
} from "./format";
import type { ProjectItem, TaskInteraction, TaskItem, WorkerItem } from "./models";

const project = { id: "project-1", name: "Project One" } as ProjectItem;
const worker = { id: "worker-1", name: "Worker One" } as WorkerItem;

describe("format helpers", () => {
  it("formats pagination boundaries", () => {
    expect(pageSummary({ offset: 0, limit: 20 }, 0)).toBe("Showing 0 of 0");
    expect(pageSummary({ offset: 20, limit: 20 }, 25)).toBe("Showing 21-25 of 25");
    expect(pageSummary({ offset: 99, limit: 20 }, 25)).toBe("Showing 25-25 of 25");
    expect(lastOffset(0, 20)).toBe(0);
    expect(lastOffset(41, 20)).toBe(40);
  });

  it("resolves project worker and task labels", () => {
    expect(projectName([project], "project-1")).toBe("Project One");
    expect(projectName([], "project-missing")).toBe("project-missing");
    expect(workerName([worker], "worker-1")).toBe("Worker One");
    expect(workerName([], "worker-missing")).toBe("worker-missing");
    expect(workerName([], undefined)).toBe("Unassigned");
    expect(taskDateRange({ startDate: "2026-08-10T00:00:00Z", endDate: "2026-08-10T00:00:00Z" } as TaskItem)).toBe("2026-08-10");
    expect(taskDateRange({ startDate: "2026-08-10", endDate: "2026-08-12" } as TaskItem)).toBe("2026-08-10 - 2026-08-12");
    expect(dateOnly()).toBe("");
  });

  it("formats and manipulates dates", () => {
    const monday = new Date("2026-08-10T12:00:00Z");
    const sunday = new Date("2026-08-09T12:00:00Z");
    expect(startOfWeek(monday).getDay()).toBe(1);
    expect(startOfWeek(sunday).getDay()).toBe(1);
    expect(addDays(monday, 2).getUTCDate()).toBe(12);
    expect(formatMonth(monday)).toContain("2026");
    expect(formatDay(monday)).toContain("2026");
    expect(parseDate("2026-08-10").getFullYear()).toBe(2026);
    expect(parseDate("not-a-date")).toBeInstanceOf(Date);
    vi.useFakeTimers();
    vi.setSystemTime(monday);
    expect(parseDate()).toEqual(monday);
    vi.useRealTimers();
  });

  it("summarizes structured and malformed interactions", () => {
    const base = { rawPayload: "" } as TaskInteraction;
    expect(interactionPayloadSummary({
      ...base,
      rawPayload: JSON.stringify({
        command: "go test ./...",
        tool_name: "Bash",
        tool_input: { file_path: "main.go" },
        cwd: "/workspace",
        reason: "verify",
      }),
    })).toContain("go test ./...");
    expect(interactionPayloadSummary({ ...base, rawPayload: '{"value":1}' })).toContain('"value": 1');
    expect(interactionPayloadSummary({ ...base, rawPayload: "{" })).toBe("{");
    expect(interactionPayloadSummary(base)).toBe("{}");
  });
});
