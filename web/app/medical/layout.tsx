import type { Metadata } from "next";
import "./medical.css";

export const metadata: Metadata = {
  title: "PulseCare — 医疗设备运维 Agent",
  description: "面向医疗设备工程与运维的受控知识检索、引用问答和质量评测工作台。",
};

export default function MedicalLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return children;
}
