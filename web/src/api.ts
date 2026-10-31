export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
  ) {
    super(message);
  }
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api${path}`, {
    credentials: 'same-origin',
    ...options,
    headers: { 'Content-Type': 'application/json', ...options.headers },
  });
  if (!response.ok) {
    let message = `요청을 처리하지 못했습니다. (${response.status})`;
    try {
      const body = await response.json();
      message = body.error || message;
    } catch {
      /* A proxy can return a non-JSON error. */
    }
    if (response.status === 401 && !path.startsWith('/login'))
      window.dispatchEvent(new Event('vgalary:unauthorized'));
    throw new ApiError(message, response.status);
  }
  if (response.status === 204) return undefined as T;
  return response.json() as Promise<T>;
}
export function post<T>(path: string, body?: unknown) {
  return api<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) });
}
