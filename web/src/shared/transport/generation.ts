let currentGeneration = 1;

export function getGeneration(): number {
  return currentGeneration;
}

export function bumpGeneration(): number {
  currentGeneration += 1;
  return currentGeneration;
}

export function resetGeneration(): void {
  currentGeneration = 1;
}
