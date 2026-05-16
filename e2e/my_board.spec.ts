import { expect, test } from "@playwright/test";

const managerGraphQL =
  process.env.BPT_MANAGER_GRAPHQL_URL || "http://localhost:8080/graphql";
const managerBaseURL =
  process.env.BPT_MANAGER_URL || managerGraphQL.replace(/\/graphql$/, "");
const managerAccessToken =
  process.env.BPT_MANAGER_TOKEN || process.env.WORKER_TOKEN || "dev-worker-token";

async function graphQL(
  request,
  query: string,
  variables: Record<string, unknown> = {},
) {
  const response = await request.post(managerGraphQL, {
    data: { query, variables },
    headers: {
      "content-type": "application/json",
      authorization: `Bearer ${managerAccessToken}`,
    },
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  expect(body.errors).toBeFalsy();
  return body.data;
}

async function openVueApp(page) {
  await page.goto("/", { waitUntil: "domcontentloaded", timeout: 120000 });
  const state = await page
    .waitForFunction(() => {
      if (document.querySelector("#app[data-ready='true']")) return "ready";
      if (document.querySelector('input[aria-label="Manager URL"]'))
        return "managers-page";
      return "";
    }, null, { timeout: 120000 })
    .then((handle) => handle.jsonValue());
  if (state === "managers-page") {
    await page.getByLabel("Manager URL").fill(managerBaseURL);
    await page.getByLabel("Manager token").fill(managerAccessToken);
    await page.getByRole("button", { name: "Add manager" }).click();
  }
  await expect(page.locator("#app[data-ready='true']")).toBeVisible({
    timeout: 120000,
  });
}

test("settings page shows current user identity", async ({ page, request }) => {
  await openVueApp(page);
  await page.getByRole("menuitem", { name: "Settings" }).click();
  await expect(page.getByText("User Identity")).toBeVisible({ timeout: 10000 });
  await expect(page.getByText("User ID")).toBeVisible();
  await expect(page.getByText("Trust Mode")).toBeVisible();
});

test("my board menu is visible and shows tasks", async ({ page, request }) => {
  const suffix = Date.now();
  const projectName = `MyBoard Project ${suffix}`;

  const created = await graphQL(
    request,
    `mutation CreateProject($input: CreateProjectInput!) {
      createProject(input: $input) { id }
    }`,
    { input: { name: projectName, gitUrl: "e2e-fixture", defaultBranch: "main", worktreeNamePrefix: "myboard" } },
  );
  const projectId = created.createProject.id;

  await graphQL(
    request,
    `mutation CreateTask($input: CreateTaskInput!) {
      createTask(input: $input) { id ownerUserId }
    }`,
    { input: { title: `MyBoard Task ${suffix}`, projectId } },
  );

  await openVueApp(page);
  const myBoardMenu = page.getByRole("menuitem", { name: "My Board" });
  await expect(myBoardMenu).toBeVisible();
  await myBoardMenu.click();
  await expect(page.getByText(`MyBoard Task ${suffix}`)).toBeVisible({ timeout: 15000 });
});

test("trust mode hides all board menu", async ({ page }) => {
  await openVueApp(page);
  const allBoardMenu = page.getByRole("menuitem", { name: "All Board" });
  await expect(allBoardMenu).toBeHidden();
});
