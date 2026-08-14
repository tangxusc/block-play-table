import { shallowMount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import WorkerStartupCommandDialog from "./WorkerStartupCommandDialog.vue";

const apiMocks = vi.hoisted(() => ({
  loadManagerBaseURL: vi.fn(),
  loadManagerToken: vi.fn(),
}));

vi.mock("../api", () => apiMocks);

const workerInput = {
  workerId: "worker-1",
  workerName: "Worker One",
  workDir: "/workspace",
  projectBindingMode: "SPECIFIC_PROJECTS",
  boundProjectIds: ["project-1"],
};

describe("WorkerStartupCommandDialog", () => {
  beforeEach(() => {
    apiMocks.loadManagerBaseURL.mockReturnValue("http://manager.local");
    apiMocks.loadManagerToken.mockReturnValue("secret");
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });

  it("builds commands and copies both variants", async () => {
    vi.useFakeTimers();
    const wrapper = shallowMount(WorkerStartupCommandDialog, {
      props: { open: true, workerInput },
    });
    const vm = wrapper.vm as unknown as {
      commands: { docker: string; shell: string };
      copiedDocker: boolean;
      copiedShell: boolean;
      copyToClipboard: (text: string, target: "docker" | "shell") => Promise<void>;
      close: () => void;
    };
    expect(vm.commands.docker).toContain("worker-1");
    expect(vm.commands.shell).toContain("manager.local");

    await vm.copyToClipboard(vm.commands.docker, "docker");
    await vm.copyToClipboard(vm.commands.shell, "shell");
    expect(navigator.clipboard.writeText).toHaveBeenCalledTimes(2);
    expect(vm.copiedDocker).toBe(true);
    expect(vm.copiedShell).toBe(true);
    vi.advanceTimersByTime(2000);
    expect(vm.copiedDocker).toBe(false);
    expect(vm.copiedShell).toBe(false);

    vm.close();
    expect(wrapper.emitted("update:open")).toEqual([[false]]);
    wrapper.findComponent({ name: "AModal" }).vm.$emit("update:open", true);
    expect(wrapper.emitted("update:open")?.[1]).toEqual([true]);
    vi.useRealTimers();
  });

  it("shows a warning without a Manager and tolerates clipboard denial", async () => {
    apiMocks.loadManagerBaseURL.mockReturnValue("");
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
    });
    const wrapper = shallowMount(WorkerStartupCommandDialog, {
      props: { open: true, workerInput },
    });
    const vm = wrapper.vm as unknown as {
      managerConfigured: boolean;
      copiedDocker: boolean;
      copyToClipboard: (text: string, target: "docker" | "shell") => Promise<void>;
    };
    expect(vm.managerConfigured).toBe(false);
    await expect(vm.copyToClipboard("command", "docker")).resolves.toBeUndefined();
    expect(vm.copiedDocker).toBe(false);
  });
});
