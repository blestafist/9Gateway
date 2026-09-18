import { UsageRangePreset, UsageBucketResolution } from "./types";

export interface RangeBounds {
  after?: string;
  before: string;
}

export interface PreviousRangeBounds {
  after: string;
  before: string;
}

export function computePresetBounds(preset: UsageRangePreset, now: Date = new Date()): RangeBounds {
  const before = now.toISOString();

  switch (preset) {
    case "1h": {
      const after = new Date(now.getTime() - 60 * 60 * 1000).toISOString();
      return { after, before };
    }
    case "today": {
      const utcMidnight = new Date(
        Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), 0, 0, 0, 0)
      );
      return { after: utcMidnight.toISOString(), before };
    }
    case "24h": {
      const after = new Date(now.getTime() - 24 * 60 * 60 * 1000).toISOString();
      return { after, before };
    }
    case "7d": {
      const after = new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000).toISOString();
      return { after, before };
    }
    case "30d": {
      const after = new Date(now.getTime() - 30 * 24 * 60 * 60 * 1000).toISOString();
      return { after, before };
    }
    case "90d": {
      const after = new Date(now.getTime() - 90 * 24 * 60 * 60 * 1000).toISOString();
      return { after, before };
    }
    case "1y": {
      const after = new Date(now.getTime() - 365 * 24 * 60 * 60 * 1000).toISOString();
      return { after, before };
    }
    case "all":
      // All retained: after is undefined so server returns all retained history
      return { before };
    case "custom":
    default:
      return { before };
  }
}

export function computePreviousBounds(bounds: RangeBounds): PreviousRangeBounds | null {
  if (!bounds.after || !bounds.before) {
    return null;
  }

  const afterMs = new Date(bounds.after).getTime();
  const beforeMs = new Date(bounds.before).getTime();

  if (Number.isNaN(afterMs) || Number.isNaN(beforeMs) || afterMs >= beforeMs) {
    return null;
  }

  const durationMs = beforeMs - afterMs;
  const prevBefore = bounds.after;
  const prevAfter = new Date(afterMs - durationMs).toISOString();

  return {
    after: prevAfter,
    before: prevBefore,
  };
}

export function getValidBuckets(
  preset: UsageRangePreset,
  customDurationMs?: number
): UsageBucketResolution[] {
  switch (preset) {
    case "1h":
    case "today":
    case "24h":
      return ["auto", "five_minutes", "hour"];
    case "7d":
    case "30d":
      return ["auto", "hour", "day"];
    case "90d":
    case "1y":
      return ["auto", "day", "week", "month"];
    case "all":
      return ["auto", "day", "week", "month"];
    case "custom": {
      if (!customDurationMs || customDurationMs <= 0) {
        return ["auto", "five_minutes", "hour", "day", "week", "month"];
      }
      const ONE_HOUR = 3600 * 1000;
      const ONE_DAY = 24 * ONE_HOUR;
      if (customDurationMs <= ONE_DAY) {
        return ["auto", "five_minutes", "hour"];
      }
      if (customDurationMs <= 31 * ONE_DAY) {
        return ["auto", "hour", "day"];
      }
      if (customDurationMs <= 730 * ONE_DAY) {
        return ["auto", "day", "week", "month"];
      }
      return ["auto", "week", "month"];
    }
    default:
      return ["auto", "hour", "day"];
  }
}

export function normalizeBucketResolution(
  preset: UsageRangePreset,
  requested: string | null | undefined,
  customDurationMs?: number
): UsageBucketResolution {
  const valid = getValidBuckets(preset, customDurationMs);
  return requested && valid.includes(requested as UsageBucketResolution)
    ? (requested as UsageBucketResolution)
    : "auto";
}

export function formatBucketLabel(bucket: UsageBucketResolution): string {
  switch (bucket) {
    case "auto":
      return "Auto";
    case "five_minutes":
      return "5 min";
    case "hour":
      return "Hourly";
    case "day":
      return "Daily";
    case "week":
      return "Weekly";
    case "month":
      return "Monthly";
    default:
      return bucket;
  }
}
