import type { Metadata } from "next";

export const metadata: Metadata = {
  title: "RAG Desk — 企业智能客服门户",
  description: "基于身份、权限、Milvus 检索与可引用生成的企业知识问答工作台。",
};

export default function PortalLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return children;
}
