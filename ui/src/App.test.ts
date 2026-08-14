import { flushPromises, shallowMount } from "@vue/test-utils";
import { beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App.vue";
import BoardPage from "./pages/BoardPage.vue";
import ManagersPage from "./pages/ManagersPage.vue";
import ProjectsPage from "./pages/ProjectsPage.vue";
import SettingsPage from "./pages/SettingsPage.vue";

const mocks = vi.hoisted(() => ({
  activeManager: null as null | { id: string; url: string; token: string; addedAt: string },
  fetchCurrentUser: vi.fn(),
}));

vi.mock("./api", () => ({
  api: { fetchCurrentUser: mocks.fetchCurrentUser },
  getActiveManager: () => mocks.activeManager,
}));

const manager = { id: "manager-1", url: "http://manager.local", token: "token", addedAt: "now" };

describe("App", () => {
  beforeEach(() => {
    document.body.innerHTML = '<div id="app"></div>';
    mocks.activeManager = null;
    mocks.fetchCurrentUser.mockResolvedValue({ id: "user-1", trustMode: false });
  });

  it("activates a Manager, loads identity and navigates pages", async () => {
    const wrapper = shallowMount(App, { attachTo: "#app" });
    expect(wrapper.findComponent(ManagersPage).props("mode")).toBe("standalone");
    wrapper.findComponent(ManagersPage).vm.$emit("activated", manager);
    await flushPromises();

    expect(document.getElementById("app")?.getAttribute("data-ready")).toBe("true");
    expect(wrapper.findComponent(BoardPage).props("ownerUserId")).toBe("user-1");
    const menu = wrapper.findComponent({ name: "AMenu" });
    menu.vm.$emit("click", { key: "projects" });
    await wrapper.vm.$nextTick();
    expect(wrapper.findComponent(ProjectsPage).exists()).toBe(true);
    menu.vm.$emit("click", { key: "settings" });
    await wrapper.vm.$nextTick();
    expect(wrapper.findComponent(SettingsPage).exists()).toBe(true);
  });

  it("starts from a configured Manager and hides the all-board entry in trust mode", async () => {
    mocks.activeManager = manager;
    mocks.fetchCurrentUser.mockResolvedValue({ id: "trusted", trustMode: true });
    const wrapper = shallowMount(App, { attachTo: "#app" });
    await flushPromises();
    expect((wrapper.vm as unknown as { ready: boolean }).ready).toBe(true);
    expect(wrapper.text()).not.toContain("All Board");
  });

  it("clears identity when loading fails or the active Manager is removed", async () => {
    mocks.activeManager = manager;
    mocks.fetchCurrentUser.mockRejectedValue(new Error("offline"));
    const wrapper = shallowMount(App, { attachTo: "#app" });
    await flushPromises();
    const vm = wrapper.vm as unknown as { currentUser: unknown; current: string; ready: boolean };
    expect(vm.currentUser).toBeNull();

    const menu = wrapper.findComponent({ name: "AMenu" });
    menu.vm.$emit("click", { key: "managers" });
    await wrapper.vm.$nextTick();
    wrapper.findComponent(ManagersPage).vm.$emit("active-cleared");
    await wrapper.vm.$nextTick();
    expect(vm.ready).toBe(false);
    expect(vm.current).toBe("myboard");
    expect(document.getElementById("app")?.hasAttribute("data-ready")).toBe(false);
  });
});
