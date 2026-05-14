import { expect, test } from "@playwright/test";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerBaseURL =
  process.env.BPT_MANAGER_URL || managerGraphQL.replace(/\/graphql$/, "");
const managerAccessToken =
  process.env.BPT_MANAGER_TOKEN ||
  process.env.WORKER_TOKEN ||
  "dev-worker-token";

const MANAGERS_KEY = "block-play-table.managers";

async function preloadManagers(
  page,
  state: {
    managers: Array<{ id: string; url: string; token: string }>;
    activeManagerId: string | null;
  },
) {
  await page.addInitScript(
    ({ key, snapshot }) => {
      if (window.localStorage.getItem(key)) return;
      const payload = {
        version: 1,
        managers: snapshot.managers.map((m) => ({
          id: m.id,
          url: m.url,
          token: m.token,
          addedAt: new Date().toISOString(),
        })),
        activeManagerId: snapshot.activeManagerId,
      };
      window.localStorage.setItem(key, JSON.stringify(payload));
    },
    { key: MANAGERS_KEY, snapshot: state },
  );
}

test("first run shows Managers entry without an active manager", async ({
  page,
}) => {
  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  await expect(page.getByLabel("Manager URL")).toBeVisible({ timeout: 120000 });
  await expect(page.getByLabel("Manager token")).toBeVisible();
  await expect(page.getByRole("button", { name: "Add manager" })).toBeVisible();
  await expect(page.locator("#app[data-ready='true']")).toHaveCount(0);
  await expect(page.locator('[data-testid="manager-row"]')).toHaveCount(0);
});

test("adds a valid manager and enters the main app", async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  await page.getByLabel("Manager URL").fill(managerBaseURL);
  await page.getByLabel("Manager token").fill(managerAccessToken);
  await page.getByRole("button", { name: "Add manager" }).click();

  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });
  await expect(page.getByRole("menuitem", { name: "Managers" })).toBeVisible();

  const stored = await page.evaluate(
    (key) => window.localStorage.getItem(key),
    MANAGERS_KEY,
  );
  expect(stored).not.toBeNull();
  const parsed = JSON.parse(stored as string);
  expect(parsed.managers).toHaveLength(1);
  expect(parsed.managers[0].url).toBe(managerBaseURL);
  expect(parsed.managers[0].token).toBe(managerAccessToken);
  expect(parsed.activeManagerId).toBe(parsed.managers[0].id);
});

test("rejects an invalid manager token and keeps the list empty", async ({
  page,
}) => {
  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  await page.getByLabel("Manager URL").fill(managerBaseURL);
  await page.getByLabel("Manager token").fill("not-a-valid-token");
  await page.getByRole("button", { name: "Add manager" }).click();

  await expect(page.getByRole("alert")).toBeVisible({ timeout: 30000 });
  await expect(page.locator('[data-testid="manager-row"]')).toHaveCount(0);
  await expect(page.locator("#app[data-ready='true']")).toHaveCount(0);

  const stored = await page.evaluate(
    (key) => window.localStorage.getItem(key),
    MANAGERS_KEY,
  );
  if (stored) {
    const parsed = JSON.parse(stored);
    expect(parsed.managers ?? []).toHaveLength(0);
  }
});

test("activates a different manager from the list", async ({ page }) => {
  const primary = {
    id: "manager-primary",
    url: managerBaseURL,
    token: managerAccessToken,
  };
  const alternate = {
    id: "manager-alternate",
    url: managerBaseURL.replace(/\/?$/, "") + "#alt",
    token: managerAccessToken,
  };
  await preloadManagers(page, {
    managers: [primary, alternate],
    activeManagerId: primary.id,
  });

  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });

  await page.getByRole("menuitem", { name: "Managers" }).click();
  const alternateRow = page.locator(
    `[data-testid="manager-row"][data-manager-id="${alternate.id}"]`,
  );
  await alternateRow.getByRole("button", { name: "Activate" }).click();

  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });
  await expect
    .poll(async () =>
      page.evaluate((key) => {
        const raw = window.localStorage.getItem(key);
        if (!raw) return null;
        const parsed = JSON.parse(raw);
        return parsed.activeManagerId;
      }, MANAGERS_KEY),
    )
    .toBe(alternate.id);
});

test("deletes an inactive manager and shrinks the list", async ({ page }) => {
  const primary = {
    id: "manager-primary",
    url: managerBaseURL,
    token: managerAccessToken,
  };
  const extra = {
    id: "manager-extra",
    url: managerBaseURL.replace(/\/?$/, "") + "#extra",
    token: managerAccessToken,
  };
  await preloadManagers(page, {
    managers: [primary, extra],
    activeManagerId: primary.id,
  });

  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });

  await page.getByRole("menuitem", { name: "Managers" }).click();
  await expect(page.locator('[data-testid="manager-row"]')).toHaveCount(2);

  const extraRow = page.locator(
    `[data-testid="manager-row"][data-manager-id="${extra.id}"]`,
  );
  await extraRow.getByRole("button", { name: "Delete" }).click();
  await page.getByRole("button", { name: "Yes" }).click();

  await expect(page.locator('[data-testid="manager-row"]')).toHaveCount(1);
  await expect(page.locator("#app[data-ready='true']")).toBeVisible();
});

test("deleting the active manager falls back to the entry page", async ({
  page,
}) => {
  const only = {
    id: "manager-only",
    url: managerBaseURL,
    token: managerAccessToken,
  };
  await preloadManagers(page, {
    managers: [only],
    activeManagerId: only.id,
  });

  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });

  await page.getByRole("menuitem", { name: "Managers" }).click();
  const row = page.locator(
    `[data-testid="manager-row"][data-manager-id="${only.id}"]`,
  );
  await row.getByRole("button", { name: "Delete" }).click();
  await page.getByRole("button", { name: "Yes" }).click();

  await expect(page.getByLabel("Manager URL")).toBeVisible({ timeout: 30000 });
  await expect(page.locator("#app[data-ready='true']")).toHaveCount(0);
});

test("persists the active manager across reloads", async ({ page }) => {
  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  await page.getByLabel("Manager URL").fill(managerBaseURL);
  await page.getByLabel("Manager token").fill(managerAccessToken);
  await page.getByRole("button", { name: "Add manager" }).click();
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });

  await page.reload({ waitUntil: "domcontentloaded" });
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });
  await expect(page.getByLabel("Manager URL")).toHaveCount(0);
});
