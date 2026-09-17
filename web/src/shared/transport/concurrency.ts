interface QueuedTask {
  run: () => void;
  reject: (err: unknown) => void;
  signal?: AbortSignal;
}

export class ConcurrencyLimiter {
  private activeReads = 0;
  private activeMutations = 0;
  private readonly readQueue: QueuedTask[] = [];
  private readonly mutationQueue: QueuedTask[] = [];

  constructor(
    public readonly maxConcurrentReads: number = 4,
    public readonly maxConcurrentMutations: number = 1
  ) {}

  get activeReadCount(): number {
    return this.activeReads;
  }

  get activeMutationCount(): number {
    return this.activeMutations;
  }

  get queuedReadCount(): number {
    return this.readQueue.length;
  }

  get queuedMutationCount(): number {
    return this.mutationQueue.length;
  }

  async acquire(isMutation: boolean, signal?: AbortSignal): Promise<() => void> {
    if (signal?.aborted) {
      throw signal.reason || new Error("Request aborted before execution");
    }

    if (isMutation) {
      if (this.activeMutations < this.maxConcurrentMutations) {
        this.activeMutations++;
        return () => this.releaseMutation();
      }
    } else {
      if (this.activeReads < this.maxConcurrentReads) {
        this.activeReads++;
        return () => this.releaseRead();
      }
    }

    return new Promise<() => void>((resolve, reject) => {
      const task: QueuedTask = {
        run: () => {
          if (isMutation) {
            this.activeMutations++;
            resolve(() => this.releaseMutation());
          } else {
            this.activeReads++;
            resolve(() => this.releaseRead());
          }
        },
        reject,
        signal,
      };

      const abortHandler = () => {
        const queue = isMutation ? this.mutationQueue : this.readQueue;
        const index = queue.indexOf(task);
        if (index !== -1) {
          queue.splice(index, 1);
          task.reject(signal?.reason || new Error("Request aborted while queued"));
        }
      };

      if (signal) {
        signal.addEventListener("abort", abortHandler, { once: true });
      }

      if (isMutation) {
        this.mutationQueue.push(task);
      } else {
        this.readQueue.push(task);
      }
    });
  }

  private releaseRead(): void {
    this.activeReads = Math.max(0, this.activeReads - 1);
    this.pumpReads();
  }

  private releaseMutation(): void {
    this.activeMutations = Math.max(0, this.activeMutations - 1);
    this.pumpMutations();
  }

  private pumpReads(): void {
    while (this.activeReads < this.maxConcurrentReads && this.readQueue.length > 0) {
      const task = this.readQueue.shift();
      if (task) {
        if (task.signal?.aborted) {
          task.reject(task.signal.reason || new Error("Request aborted"));
          continue;
        }
        task.run();
      }
    }
  }

  private pumpMutations(): void {
    while (this.activeMutations < this.maxConcurrentMutations && this.mutationQueue.length > 0) {
      const task = this.mutationQueue.shift();
      if (task) {
        if (task.signal?.aborted) {
          task.reject(task.signal.reason || new Error("Request aborted"));
          continue;
        }
        task.run();
      }
    }
  }

  reset(): void {
    this.activeReads = 0;
    this.activeMutations = 0;
    while (this.readQueue.length > 0) {
      const task = this.readQueue.shift();
      task?.reject(new Error("Concurrency limiter reset"));
    }
    while (this.mutationQueue.length > 0) {
      const task = this.mutationQueue.shift();
      task?.reject(new Error("Concurrency limiter reset"));
    }
  }
}

export const defaultConcurrencyLimiter = new ConcurrencyLimiter(4, 1);
