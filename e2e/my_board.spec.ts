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

async function fillTextField(page, input, value: string) {
  for (let attempt = 0; attempt < 3; attempt++) {
    await input.click();
    await page.waitForTimeout(100);
    await input.fill(value);
    await page.waitForTimeout(100);
    if ((await input.inputValue()) === value) {
      return;
    }
  }
  await expect(input).toHaveValue(value);
}

async function selectFieldOption(page, field, optionName: string) {
  await field.click();
  const roleOption = page.getByRole("option", { name: optionName });
  if (await roleOption.isVisible({ timeout: 1000 }).catch(() => false)) {
    await roleOption.click();
    return;
  }
  await page.getByText(optionName, { exact: true }).last().click();
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
  await expect(page.getByRole("heading", { name: "User Identity" })).toBeVisible({ timeout: 10000 });
  await expect(page.getByText("User ID", { exact: true })).toBeVisible();
  await expect(page.getByText("Trust Mode", { exact: true })).toBeVisible();
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

test("create task with employee selection defaults to current user", async ({ page, request }) => {
  const suffix = Date.now();
  const projectName = `EmpSelect Project ${suffix}`;

  await graphQL(
    request,
    `mutation CreateProject($input: CreateProjectInput!) {
      createProject(input: $input) { id }
    }`,
    { input: { name: projectName, gitUrl: "e2e-fixture", defaultBranch: "main", worktreeNamePrefix: "emp" } },
  );

  await openVueApp(page);
  const myBoardMenu = page.getByRole("menuitem", { name: "My Board" });
  await myBoardMenu.click();

  await page.getByRole("button", { name: "New task" }).click();
  const createTaskDialog = page.getByRole("dialog", { name: "Create task" });
  await expect(createTaskDialog).toBeVisible();

  const employeeField = createTaskDialog.getByLabel("Employee");
  await expect(employeeField).toBeVisible();
  const employeeFormItem = createTaskDialog.locator(".ant-form-item").filter({ hasText: "Employee" });
  await expect(employeeFormItem.locator(".ant-select-selection-item")).toHaveText("Trust Mode User");

  await fillTextField(page, createTaskDialog.getByLabel("Title"), `EmpTask ${suffix}`);
  await selectFieldOption(page, createTaskDialog.getByLabel("Project"), projectName);

  await createTaskDialog.getByRole("button", { name: "Save" }).click();
  await expect(createTaskDialog).toBeHidden();

  const result = await graphQL(
    request,
    `query TaskList($filter: TaskFilter) { taskList(filter: $filter) { id title ownerUserId } }`,
    { filter: { search: `EmpTask ${suffix}` } },
  );
  const task = result.taskList.find((t) => t.title === `EmpTask ${suffix}`);
  expect(task).toBeTruthy();
  expect(task.ownerUserId).toBe("trust-mode-user-id");
});
