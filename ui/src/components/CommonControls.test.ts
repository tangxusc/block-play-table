import { mount, shallowMount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import AgentConfigFields from "./AgentConfigFields.vue";
import PageHeader from "./PageHeader.vue";
import PaginationBar from "./PaginationBar.vue";
import SearchToolbar from "./SearchToolbar.vue";
import { emptyAgentConfigDraft } from "../agentConfig";

describe("shared controls", () => {
  it("renders the page header and named actions", () => {
    const wrapper = mount(PageHeader, {
      props: { title: "Workers" },
      slots: { actions: "Refresh" },
    });
    expect(wrapper.get("h1").text()).toBe("Workers");
    expect(wrapper.text()).toContain("Refresh");
  });

  it("moves pagination within the valid bounds", async () => {
    const wrapper = shallowMount(PaginationBar, {
      props: { page: { offset: 20, limit: 20 }, total: 55 },
    });
    const vm = wrapper.vm as unknown as { previous: () => void; next: () => void };
    vm.previous();
    vm.next();
    expect(wrapper.emitted("change")).toEqual([
      [{ offset: 0, limit: 20 }],
      [{ offset: 40, limit: 20 }],
    ]);

    await wrapper.setProps({ page: { offset: 0, limit: 20 }, total: 0 });
    vm.previous();
    vm.next();
    expect(wrapper.emitted("change")?.slice(-2)).toEqual([
      [{ offset: 0, limit: 20 }],
      [{ offset: 0, limit: 20 }],
    ]);
  });

  it("emits search, project and sort changes", () => {
    const wrapper = shallowMount(SearchToolbar, {
      props: {
        search: "old",
        sort: { field: "NAME", direction: "DESC" },
        sortOptions: [{ field: "NAME", label: "Name" }],
        projects: [
          {
            id: "project-1",
            name: "Project",
            gitUrl: "repo",
            defaultBranch: "main",
            worktreeNamePrefix: "task",
            archived: false,
            createdAt: "now",
            updatedAt: "now",
          },
        ],
      },
    });
    const vm = wrapper.vm as unknown as {
      toggleDirection: () => void;
      popupContainer: () => HTMLElement;
    };
    vm.toggleDirection();
    expect(wrapper.emitted("update:sort")?.[0]).toEqual([{ field: "NAME", direction: "ASC" }]);
    expect(vm.popupContainer()).toBe(document.body);

    const input = wrapper.findComponent({ name: "AInputSearch" });
    input.vm.$emit("update:value", 42);
    input.vm.$emit("search", "new");
    const selects = wrapper.findAllComponents({ name: "ASelect" });
    selects[0].vm.$emit("change", "project-1");
    selects[0].vm.$emit("clear");
    selects[1].vm.$emit("change", "CREATED_AT");
    expect(wrapper.emitted("update:search")).toEqual([["42"], ["new"]]);
    expect(wrapper.emitted("update:project")).toEqual([["project-1"], [""]]);
    expect(wrapper.emitted("update:sort")?.[1]).toEqual([{ field: "CREATED_AT", direction: "DESC" }]);
  });

  it("switches the sort direction from ascending to descending", () => {
    const wrapper = shallowMount(SearchToolbar, {
      props: {
        search: "",
        sort: { field: "NAME", direction: "ASC" },
        sortOptions: [{ field: "NAME", label: "Name" }],
      },
    });
    (wrapper.vm as unknown as { toggleDirection: () => void }).toggleDirection();
    expect(wrapper.emitted("update:sort")?.[0]).toEqual([{ field: "NAME", direction: "DESC" }]);
  });

  it("renders Codex and Claude runtime fields", async () => {
    const draft = emptyAgentConfigDraft();
    const wrapper = shallowMount(AgentConfigFields, {
      props: { agentType: "codex", draft },
    });
    expect(wrapper.text()).toContain("Bypass approvals and sandbox");
    expect(wrapper.text()).toContain("Danger full access");
    wrapper.findAllComponents({ name: "ASelect" }).forEach((select, index) => {
      select.vm.$emit("update:value", ["IMPLEMENT", "HIGH", "WORKSPACE_WRITE", "ON_REQUEST"][index]);
    });
    wrapper.findComponent({ name: "AInput" }).vm.$emit("update:value", "gpt-test");
    wrapper.findAllComponents({ name: "ACheckbox" }).forEach((checkbox) => checkbox.vm.$emit("update:checked", true));
    expect(draft).toMatchObject({
      workMode: "IMPLEMENT",
      codexModel: "gpt-test",
      codexReasoningEffort: "HIGH",
      codexSandboxMode: "WORKSPACE_WRITE",
      codexApprovalPolicy: "ON_REQUEST",
      codexFullAuto: true,
      codexBypassApprovalsAndSandbox: true,
    });

    await wrapper.setProps({ agentType: "claude" });
    expect(wrapper.text()).toContain("Don't ask");
    expect(wrapper.text()).toContain("Max");
    expect(wrapper.text()).not.toContain("Bypass approvals and sandbox");
    const claudeSelects = wrapper.findAllComponents({ name: "ASelect" });
    claudeSelects[1].vm.$emit("update:value", "MAX");
    claudeSelects[2].vm.$emit("update:value", "DONT_ASK");
    wrapper.findComponent({ name: "AInput" }).vm.$emit("update:value", "claude-test");
    expect(draft).toMatchObject({ claudeModel: "claude-test", claudeEffort: "MAX", claudePermissionMode: "DONT_ASK" });

    await wrapper.setProps({ agentType: "custom" });
    expect(wrapper.text()).not.toContain("Don't ask");
    expect(wrapper.text()).not.toContain("Full auto");
  });
});
