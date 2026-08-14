import { afterEach, vi } from "vitest";
import { config, enableAutoUnmount } from "@vue/test-utils";
import { defineComponent } from "vue";

class ResizeObserverStub {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}

class IntersectionObserverStub {
  readonly root = null;
  readonly rootMargin = "0px";
  readonly thresholds = [0];

  disconnect(): void {}
  observe(): void {}
  takeRecords(): IntersectionObserverEntry[] {
    return [];
  }
  unobserve(): void {}
}

vi.stubGlobal("ResizeObserver", ResizeObserverStub);
vi.stubGlobal("IntersectionObserver", IntersectionObserverStub);
vi.stubGlobal(
  "matchMedia",
  vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
);

Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: globalThis.matchMedia,
});

Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
  configurable: true,
  value: vi.fn(() => null),
});

const antComponentNames = [
  "a-alert",
  "a-button",
  "a-checkbox",
  "a-checkbox-group",
  "a-date-picker",
  "a-descriptions",
  "a-descriptions-item",
  "a-divider",
  "a-empty",
  "a-form",
  "a-form-item",
  "a-input",
  "a-input-password",
  "a-input-search",
  "a-layout",
  "a-layout-sider",
  "a-menu",
  "a-menu-item",
  "a-modal",
  "a-popconfirm",
  "a-radio-button",
  "a-radio-group",
  "a-select",
  "a-select-option",
  "a-space",
  "a-spin",
  "a-tab-pane",
  "a-table",
  "a-tabs",
  "a-tag",
  "a-textarea",
  "a-tooltip",
];

config.global.components = Object.fromEntries(
  antComponentNames.map((name) => [
    name,
    defineComponent({
      name: name
        .split("-")
        .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
        .join(""),
      template: "<div><slot name=\"title\" /><slot /></div>",
    }),
  ]),
);

enableAutoUnmount(afterEach);
config.global.renderStubDefaultSlot = true;

afterEach(() => {
  vi.clearAllMocks();
  window.localStorage.clear();
  window.sessionStorage.clear();
});
