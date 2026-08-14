import { shallowMount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import TerminalPanel from "./TerminalPanel.vue";

const terminalMocks = vi.hoisted(() => ({
  dispose: vi.fn(),
  open: vi.fn(),
  write: vi.fn(),
  writeln: vi.fn(),
  onData: vi.fn(),
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: vi.fn(function Terminal() {
    return terminalMocks;
  }),
}));

class SocketStub {
  static instances: SocketStub[] = [];
  listeners = new Map<string, Array<(event: { data?: unknown }) => void>>();
  send = vi.fn();
  close = vi.fn();

  constructor(public readonly url: string) {
    SocketStub.instances.push(this);
  }

  addEventListener(type: string, listener: (event: { data?: unknown }) => void): void {
    const listeners = this.listeners.get(type) || [];
    listeners.push(listener);
    this.listeners.set(type, listeners);
  }

  emit(type: string, event: { data?: unknown } = {}): void {
    for (const listener of this.listeners.get(type) || []) listener(event);
  }
}

describe("TerminalPanel", () => {
  beforeEach(() => {
    SocketStub.instances = [];
    vi.stubGlobal("WebSocket", SocketStub);
    Object.values(terminalMocks).forEach((mock) => mock.mockReset());
  });

  it("connects, exchanges terminal data and disposes resources", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ ok: true }));
    const wrapper = shallowMount(TerminalPanel, {
      props: { checkUrl: "/check", socketUrl: "ws://terminal" },
      attachTo: document.body,
    });
    const vm = wrapper.vm as unknown as { connect: () => Promise<void>; state: string; error: string };
    await vm.connect();
    const socket = SocketStub.instances[0];
    expect(socket.url).toBe("ws://terminal");
    socket.emit("open");
    expect(vm.state).toBe("connected");
    socket.emit("message", { data: JSON.stringify({ type: "output", data: "hello" }) });
    expect(terminalMocks.write).toHaveBeenCalledWith("hello");
    socket.emit("close");
    expect(terminalMocks.writeln).toHaveBeenCalledWith("\r\nDisconnected");

    const onData = terminalMocks.onData.mock.calls[0][0] as (data: string) => void;
    onData("ls\n");
    expect(socket.send).toHaveBeenCalledWith(JSON.stringify({ type: "input", data: "ls\n" }));

    socket.emit("message", { data: JSON.stringify({ type: "error", data: "remote failed" }) });
    expect(vm.state).toBe("error");
    expect(vm.error).toBe("remote failed");
    wrapper.unmount();
    expect(socket.close).toHaveBeenCalled();
    expect(terminalMocks.dispose).toHaveBeenCalled();
  });

  it("reports HTTP and non-Error connection failures", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: false,
      status: 503,
      text: vi.fn().mockResolvedValue(""),
    }));
    const wrapper = shallowMount(TerminalPanel, {
      props: { checkUrl: "/check", socketUrl: "ws://terminal" },
    });
    const vm = wrapper.vm as unknown as { connect: () => Promise<void>; state: string; error: string };
    await vm.connect();
    expect(vm.state).toBe("error");
    expect(vm.error).toBe("Terminal check failed (503)");

    vi.mocked(fetch).mockRejectedValueOnce("offline");
    await vm.connect();
    expect(vm.error).toBe("offline");
  });
});
