export function createMockRequest(
  url: string,
  options: RequestInit & { headers?: Record<string, string> } = {}
): Request {
  const headers = new Headers(options.headers);

  return new Request(`http://localhost:3000${url}`, {
    method: options.method || "GET",
    headers,
    body: options.body,
  });
}

export function createMockFormData(data: Record<string, string>): FormData {
  const formData = new FormData();
  Object.entries(data).forEach(([key, value]) => {
    formData.append(key, value);
  });
  return formData;
}
