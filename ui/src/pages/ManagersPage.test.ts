import { shallowMount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import ManagersPage from "./ManagersPage.vue";

const mocks = vi.hoisted(() => ({
  state: {
    version: 1,
    managers: [] as Array<{ id: string; url: string; token: string; addedAt: string }>,
    activeManagerId: null as string | null,
  },
  addManager: vi.fn(),
  setActiveManager: vi.fn(),
  removeManager: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}));

vi.mock("ant-design-vue", () => ({
  message: { success: mocks.success, error: mocks.error },
}));

vi.mock("../api", () => ({
  addManager: mocks.addManager,
  getActiveManager: () => mocks.state.managers.find((entry) => entry.id === mocks.state.activeManagerId) || null,
  listManagers: () => [...mocks.state.managers],
  loadManagersState: () => ({ ...mocks.state, managers: [...mocks.state.managers] }),
  maskToken: (token: string) => token ? `masked-${token.slice(-4)}` : "",
  removeManager: (id: string) => {
    mocks.removeManager(id);
    mocks.state.managers = mocks.state.managers.filter((entry) => entry.id !== id);
    if (mocks.state.activeManagerId === id) mocks.state.activeManagerId = null;
  },
  setActiveManager: mocks.setActiveManager,
}));

const first = { id: "manager-1", url: "http://one.local", token: "token-one", addedAt: "now" };
const second = { id: "manager-2", url: "http://two.local", token: "", addedAt: "now" };

describe("ManagersPage", () => {
  beforeEach(() => {
    mocks.state.managers = [first, second];
    mocks.state.activeManagerId = first.id;
    mocks.addManager.mockImplementation(async () => {
      const entry = { id: "manager-3", url: "http://three.local", token: "token", addedAt: "now" };
      mocks.state.managers.push(entry);
      mocks.state.activeManagerId = entry.id;
      return entry;
    });
    mocks.setActiveManager.mockImplementation((id: string) => {
      mocks.state.activeManagerId = id;
    });
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
  });

  it("adds, refreshes and deletes Manager entries", async () => {
    const wrapper = shallowMount(ManagersPage, { props: { mode: "embedded" } });
    const vm = wrapper.vm as unknown as {
      formURL: string;
      formToken: string;
      managers: typeof mocks.state.managers;
      activeId: string | null;
      handleAdd: () => Promise<void>;
      handleDelete: (entry: typeof first) => void;
      refresh: () => void;
    };
    vm.formURL = "http://three.local";
    vm.formToken = "token";
    await vm.handleAdd();
    expect(wrapper.emitted("activated")?.[0]?.[0]).toMatchObject({ id: "manager-3" });
    expect(vm.formURL).toBe("");
    expect(vm.activeId).toBe("manager-3");

    vm.handleDelete(second);
    expect(wrapper.emitted("active-cleared")).toBeUndefined();
    vm.handleDelete(mocks.state.managers.find((entry) => entry.id === "manager-3")!);
    expect(wrapper.emitted("active-cleared")).toEqual([[]]);
    expect(mocks.removeManager).toHaveBeenCalledTimes(2);
  });

  it("reports Error and string add failures", async () => {
    mocks.addManager.mockRejectedValueOnce(new Error("invalid endpoint"));
    const wrapper = shallowMount(ManagersPage, { props: { mode: "standalone" } });
    const vm = wrapper.vm as unknown as { handleAdd: () => Promise<void>; error: string; submitting: boolean };
    await vm.handleAdd();
    expect(vm.error).toBe("invalid endpoint");
    expect(vm.submitting).toBe(false);

    mocks.addManager.mockRejectedValueOnce("offline");
    await vm.handleAdd();
    expect(vm.error).toBe("offline");
  });

  it("handles activation guards and clipboard outcomes", async () => {
    const wrapper = shallowMount(ManagersPage, { props: { mode: "embedded" } });
    const vm = wrapper.vm as unknown as {
      handleActivate: (entry: typeof first) => void;
      handleCopy: (token: string) => Promise<void>;
    };
    vm.handleActivate(first);
    expect(mocks.setActiveManager).not.toHaveBeenCalled();
    await vm.handleCopy("");
    await vm.handleCopy(first.token);
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(first.token);
    expect(mocks.success).toHaveBeenCalledWith("Token copied");

    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
    });
    await vm.handleCopy(first.token);
    expect(mocks.error).toHaveBeenCalledWith("Copy failed");

    Object.defineProperty(navigator, "clipboard", { configurable: true, value: {} });
    await expect(vm.handleCopy(first.token)).resolves.toBeUndefined();
  });

  it("renders standalone and embedded empty states", () => {
    const standalone = shallowMount(ManagersPage, { props: { mode: "standalone" } });
    expect(standalone.text()).toContain("Saved managers");
    standalone.unmount();
    mocks.state.managers = [];
    mocks.state.activeManagerId = null;
    const embedded = shallowMount(ManagersPage, { props: { mode: "embedded" } });
    expect(embedded.findComponent({ name: "AEmpty" }).exists()).toBe(true);
  });
});
