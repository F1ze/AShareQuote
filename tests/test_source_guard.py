import importlib.util
from pathlib import Path
import socket
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location("source_worker", ROOT / "internal/platform/baostock/source_worker.py")
worker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(worker)


class SDKGuardTest(unittest.TestCase):
    def test_eof_is_an_error_instead_of_an_infinite_recv_loop(self):
        left, right = socket.socketpair()
        guarded = worker.CheckedSocket(fileno=left.detach())
        right.close()
        try:
            with self.assertRaises(ConnectionError):
                guarded.recv(8192)
        finally:
            guarded.close()

    def test_transport_failure_is_not_silently_truncated_history(self):
        with patch.object(worker, "sdk_send", return_value=None):
            with self.assertRaises(ConnectionError):
                worker.checked_send("request")

    def test_repeated_page_is_rejected(self):
        class RepeatedPage:
            error_code = "0"
            cur_row_num = 0
            cur_page_num = "1"
            fields = ["code"]
            def next(self):
                return True
            def get_row_data(self):
                return ["sh.600000"]
        with self.assertRaises(ValueError):
            worker.collect(RepeatedPage())
