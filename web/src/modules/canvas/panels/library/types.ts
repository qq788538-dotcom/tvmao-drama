export interface LibraryFolder {
  id: number;
  parentId?: number;
  name: string;
  itemCount: number;
  createdAt: string;
  updatedAt: string;
}

export type LibraryItemKind = 'image' | 'video' | 'audio';
export type LibraryItemSource = 'upload' | 'from_project';

export interface LibraryItem {
  id: number;
  folderId: number;
  url: string;
  sizeBytes: number;
  mimeType: string;
  kind: LibraryItemKind;
  width?: number;
  height?: number;
  durationMs?: number;
  source: LibraryItemSource;
  name: string;
  createdAt: string;
}
