const millisecondsPerSecond = 1_000;
const legacyFutureYears = 50;
const yearsPerCentury = 100;

// RFC 9110 section 10.2.3 allows nonnegative integer seconds or an HTTP date.
export function retryAfterDelay(header: string | undefined): number | undefined {
  const value = header?.trim();
  if (!value) return undefined;
  if (/^\d+$/.test(value)) return Number(value) * millisecondsPerSecond;

  // HTTP dates begin with a weekday. Exclude numeric strings Date.parse treats as dates.
  if (!/^[A-Za-z]+[ ,]/.test(value) || !value.includes(":")) return undefined;

  const date = httpDate(value);
  return Number.isNaN(date) ? undefined : Math.max(0, date - Date.now());
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
