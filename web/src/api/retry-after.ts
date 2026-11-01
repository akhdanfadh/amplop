const millisecondsPerSecond = 1_000;
const legacyFutureYears = 50;
const yearsPerCentury = 100;

// retryAfterDelay returns how many milliseconds from now a Retry-After header
// asks a client to wait, at least 0, or undefined when the header is absent
// or unusable.
export function retryAfterDelay(header: string | undefined): number | undefined {
  const now = Date.now();
  const time = retryAfterTime(header, now);
  return time === undefined ? undefined : Math.max(0, time - now);
}

// retryAfterTime returns when a Retry-After header received at receivedAt
// allows a retry, in milliseconds since the epoch, or undefined when the
// header is absent or unusable. RFC 9110 section 10.2.3 allows nonnegative
// integer seconds or an HTTP date.
export function retryAfterTime(header: string | undefined, receivedAt: number): number | undefined {
  const value = header?.trim();
  if (!value) return undefined;
  if (/^\d+$/.test(value)) return receivedAt + Number(value) * millisecondsPerSecond;

  // HTTP dates begin with a weekday. Exclude numeric strings Date.parse treats as dates.
  if (!/^[A-Za-z]+[ ,]/.test(value) || !value.includes(":")) return undefined;

  const date = httpDate(value);
  return Number.isNaN(date) ? undefined : date;
}

// RFC 9110 section 5.6.7 uses a moving fifty-year window for legacy two-digit years.
function httpDate(value: string): number {
  // The legacy asctime form has no timezone; HTTP still defines it as GMT.
  const date = new Date(value.endsWith("GMT") ? value : `${value} GMT`);
  if (!/^[A-Za-z]+, \d{2}-[A-Za-z]{3}-\d{2} /.test(value)) return date.getTime();

  const limit = new Date();
  limit.setUTCFullYear(limit.getUTCFullYear() + legacyFutureYears);
  const century = limit.getUTCFullYear() - (limit.getUTCFullYear() % yearsPerCentury);
  date.setUTCFullYear(century + (date.getUTCFullYear() % yearsPerCentury));
  if (date.getTime() > limit.getTime())
    date.setUTCFullYear(date.getUTCFullYear() - yearsPerCentury);

  return date.getTime();
}
