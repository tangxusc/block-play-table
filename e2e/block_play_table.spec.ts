import { expect, test } from '@playwright/test';

const managerGraphQL = process.env.BPT_MANAGER_GRAPHQL_URL || 'http://localhost:8080/graphql';

async function graphQL(request, query: string, variables: Record<string, unknown> = {}) {
  const response = await request.post(managerGraphQL, {
    data: { query, variables },
    headers: { 'content-type': 'application/json' }
  });
  expect(response.ok()).toBeTruthy();
  const body = await response.json();
  expect(body.errors).toBeFalsy();
  return body.data;
}

test('trusted Flutter web UI creates project and task', async ({ page, request }) => {
  await page.setViewportSize({ width: 1400, height: 900 });
  await page.goto('/');
  await expect(page).toHaveTitle('Block Play Table');
  await expect(page.locator('flutter-view')).toBeVisible();
  await page.waitForTimeout(1500);

  const suffix = Date.now();
  const projectName = `E2E Project ${suffix}`;
  const taskTitle = `E2E Task ${suffix}`;

  await page.mouse.click(40, 208);
  await page.waitForTimeout(500);
  await page.mouse.click(190, 96);
  await page.keyboard.type(projectName);
  await page.mouse.click(500, 96);
  await page.keyboard.type('e2e-fixture');
  await page.mouse.click(986, 88);

  await expect.poll(async () => {
    const data = await graphQL(request, 'query { projects { name } }');
    return data.projects.some((project: { name: string }) => project.name === projectName);
  }).toBeTruthy();

  await page.mouse.click(40, 80);
  await page.waitForTimeout(500);
  await page.mouse.click(210, 96);
  await page.keyboard.type(taskTitle);
  await page.mouse.click(922, 96);

  await expect.poll(async () => {
    const data = await graphQL(request, 'query { tasks { title status } }');
    return data.tasks.some((task: { title: string; status: string }) => task.title === taskTitle && task.status === 'CREATED');
  }).toBeTruthy();
});
