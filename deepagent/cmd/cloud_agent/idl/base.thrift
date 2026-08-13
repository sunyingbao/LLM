namespace go base
namespace py base
namespace java com.bytedance.thrift.base

struct Base {
  1: string LogID = "",
  2: string Caller = "",
  3: string Addr = "",
  4: string client = "",
}

struct BaseResp {
  1: string StatusMessage = "",
  2: i32 StatusCode = 0,
}
