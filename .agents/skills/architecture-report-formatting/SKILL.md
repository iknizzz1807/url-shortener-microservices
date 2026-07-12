---
name: architecture-report-formatting
description: Rút gọn và định dạng lại tài liệu báo cáo kiến trúc hệ thống, lược bỏ mã nguồn chi tiết và sử dụng biểu đồ Mermaid thay cho ASCII.
---

# Hướng Dẫn Định Dạng Báo Cáo Kiến Trúc Hệ Thống (Architecture Report Formatting)

Tài liệu này hướng dẫn cách rút gọn và tối ưu hóa các báo cáo thiết kế hệ thống và kiến trúc phần mềm, chuyển đổi từ các tài liệu nặng về mã nguồn triển khai sang tài liệu súc tích, tập trung vào thiết kế và mô hình hóa.

## Quy Tắc Cốt Lõi

### 1. Tập Trung Vào Kiến Trúc (Architecture-First)
- Giữ lại các phân tích cấp cao: Domain-Driven Design (DDD), phân chia Bounded Contexts, sơ đồ liên kết (Context Mapping), Aggregate Roots (schema, invariants, business rules), Ubiquitous Language, và các Quyết định thiết kế chiến lược (Strategic Decisions).
- Giữ lại các bảng đánh giá bảo mật (Threat Assessment), phân tích hiệu năng (Performance Analysis), điểm mạnh, điểm yếu và khuyến nghị cải thiện.

### 2. Loại Bỏ Mã Nguồn Chi Tiết (No Implementation Code)
- Loại bỏ toàn bộ các khối mã nguồn chi tiết (Go code blocks, Dockerfiles, YAML files, unit tests).
- Loại bỏ phân tích chi tiết dòng code không cần thiết.
- Thay thế các đoạn code hoặc cấu trúc file chi tiết bằng các bảng tóm tắt, sơ đồ tuần tự hoặc văn bản ngắn gọn.
- Không ghi tên file code (`.go`, `.js`, v.v.) trực tiếp trên tiêu đề các mục (ví dụ: Thay vì `3.5 Request Handler (handler.go)`, sử dụng `3.5 Request Handler`).

### 3. Sử Dụng Biểu Đồ Mermaid Thay Thế ASCII (Mermaid over ASCII)
- Không vẽ sơ đồ bằng ký tự chữ ASCII. Thay thế hoàn toàn bằng cú pháp biểu đồ **Mermaid** tương ứng trong Markdown.
- Sử dụng các loại biểu đồ Mermaid phù hợp:
  - **Sơ đồ kiến trúc/tổ chức**: `graph TD` hoặc `graph LR` (kèm định nghĩa CSS color class cho Database, Services, Broker).
  - **Sơ đồ luồng/Event Storming**: `flowchart TD` (phân chia subgraph rõ ràng).
  - **Sơ đồ tuần tự/luồng Request**: `sequenceDiagram` (ghi chú rõ các Middleware, Controller, Service, DB transaction).
  - **Sơ đồ trạng thái**: `stateDiagram-v2` (dùng cho Circuit Breaker hoặc các State Machine khác).

### 4. Bố Cục Dàn Trang Đứng (Vertical Stacking)
- Đối với các sơ đồ Mermaid chứa nhiều luồng xử lý hoặc subgraph độc lập (như Event Storming), sử dụng các liên kết ẩn (cú pháp `FlowA ~~~ FlowB`) để ép buộc layout engine sắp xếp các subgraph chồng lên nhau theo **chiều dọc**.
- Tránh để các subgraph độc lập dàn hàng ngang (horizontal) gây khó đọc và tràn màn hình trên các thiết bị hiển thị hẹp.
